package main

// Reads the Apple System Management Controller (temperatures, fans, power)
// through IOKit. Called via purego so the server still builds without cgo.

import (
	"encoding/binary"
	"fmt"
	"math"
	"runtime"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// Offsets inside the 80-byte SMCKeyData_t parameter block.
const (
	smcSize      = 80
	offKey       = 0
	offDataSize  = 28
	offDataType  = 32
	offResult    = 40
	offData8     = 42
	offData32    = 44
	offBytes     = 48
	smcSelector  = 2 // kSMCHandleYPCEvent
	cmdReadBytes = 5
	cmdReadIndex = 8
	cmdReadInfo  = 9
)

type smcKey struct {
	name string
	code uint32
	typ  string
	size uint32
}

var smc struct {
	once sync.Once
	mu   sync.Mutex
	ok   bool
	conn uint32
	call func(conn uint32, selector uint32, in unsafe.Pointer, inSize uintptr, out unsafe.Pointer, outSize *uintptr) int32

	temps []smcKey
	fans  int
	power *smcKey
}

func fourcc(s string) uint32 { return binary.BigEndian.Uint32([]byte(s)) }

func fourccString(v uint32) string {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, v)
	return string(b)
}

func smcOpen() bool {
	smc.once.Do(func() {
		defer func() { recover() }() // a missing symbol must never take the server down
		iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		libsys, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
		if err != nil {
			return
		}
		var matching func(name string) uintptr
		var getService func(mainPort uint32, matching uintptr) uint32
		var open func(service uint32, task uint32, kind uint32, conn *uint32) int32
		var release func(obj uint32) int32
		purego.RegisterLibFunc(&matching, iokit, "IOServiceMatching")
		purego.RegisterLibFunc(&getService, iokit, "IOServiceGetMatchingService")
		purego.RegisterLibFunc(&open, iokit, "IOServiceOpen")
		purego.RegisterLibFunc(&release, iokit, "IOObjectRelease")
		purego.RegisterLibFunc(&smc.call, iokit, "IOConnectCallStructMethod")
		taskPtr, err := purego.Dlsym(libsys, "mach_task_self_")
		if err != nil {
			return
		}
		// taskPtr is the address of a C global, not Go memory; go through a
		// pointer-to-pointer so vet's unsafe.Pointer check stays meaningful.
		task := **(**uint32)(unsafe.Pointer(&taskPtr))

		service := getService(0, matching("AppleSMC"))
		if service == 0 {
			return
		}
		defer release(service)
		if open(service, task, 0, &smc.conn) != 0 {
			return
		}
		smc.ok = true
		smcDiscover()
	})
	return smc.ok
}

func smcCall(in *[smcSize]byte) (out [smcSize]byte, ok bool) {
	size := uintptr(smcSize)
	if smc.call(smc.conn, smcSelector, unsafe.Pointer(in), smcSize, unsafe.Pointer(&out), &size) != 0 {
		return out, false
	}
	return out, out[offResult] == 0
}

func smcInfo(code uint32) (smcKey, bool) {
	var in [smcSize]byte
	binary.LittleEndian.PutUint32(in[offKey:], code)
	in[offData8] = cmdReadInfo
	out, ok := smcCall(&in)
	if !ok {
		return smcKey{}, false
	}
	return smcKey{
		name: fourccString(code),
		code: code,
		typ:  fourccString(binary.LittleEndian.Uint32(out[offDataType:])),
		size: binary.LittleEndian.Uint32(out[offDataSize:]),
	}, true
}

func smcRead(k smcKey) (float64, bool) {
	var in [smcSize]byte
	binary.LittleEndian.PutUint32(in[offKey:], k.code)
	binary.LittleEndian.PutUint32(in[offDataSize:], k.size)
	in[offData8] = cmdReadBytes
	out, ok := smcCall(&in)
	if !ok {
		return 0, false
	}
	b := out[offBytes:]
	switch k.typ {
	case "flt ":
		return float64(math.Float32frombits(binary.LittleEndian.Uint32(b))), true
	case "sp78":
		return float64(int16(binary.BigEndian.Uint16(b))) / 256, true
	case "fpe2":
		return float64(binary.BigEndian.Uint16(b)) / 4, true
	case "ui8 ":
		return float64(b[0]), true
	case "ui16":
		return float64(binary.BigEndian.Uint16(b)), true
	case "ui32":
		return float64(binary.BigEndian.Uint32(b)), true
	}
	return 0, false
}

func smcReadName(name string) (float64, bool) {
	k, ok := smcInfo(fourcc(name))
	if !ok {
		return 0, false
	}
	return smcRead(k)
}

// smcDiscover walks the SMC key table once and remembers the temperature keys.
func smcDiscover() {
	count, ok := smcReadName("#KEY")
	if !ok {
		return
	}
	for i := uint32(0); i < uint32(count); i++ {
		var in [smcSize]byte
		in[offData8] = cmdReadIndex
		binary.LittleEndian.PutUint32(in[offData32:], i)
		out, ok := smcCall(&in)
		if !ok {
			continue
		}
		code := binary.LittleEndian.Uint32(out[offKey:])
		if byte(code>>24) != 'T' {
			continue
		}
		k, ok := smcInfo(code)
		if !ok || (k.typ != "flt " && k.typ != "sp78") {
			continue
		}
		if v, ok := smcRead(k); ok && v > 5 && v < 130 {
			smc.temps = append(smc.temps, k)
		}
	}
	if n, ok := smcReadName("FNum"); ok {
		smc.fans = int(n)
	}
	if k, ok := smcInfo(fourcc("PSTR")); ok {
		if v, ok := smcRead(k); ok && v > 0 && v < 1000 {
			smc.power = &k
		}
	}
}

// smcKind classifies a temperature key. Apple does not document the keys and
// they differ per chip, so only the prefixes that have stayed stable are
// trusted: on Apple silicon Tp/Te are performance/efficiency cores and Tg the
// GPU; on Intel TC is the CPU and TG the GPU.
func smcKind(name string) string {
	arm := runtime.GOARCH == "arm64"
	switch name[:2] {
	case "Tp", "Te":
		if arm {
			return "cpu"
		}
	case "TC":
		if !arm {
			return "cpu"
		}
	case "Tg":
		if arm {
			return "gpu"
		}
	case "TG":
		if !arm {
			return "gpu"
		}
	case "TB":
		return "battery"
	case "TH":
		return "ssd"
	}
	return ""
}

func platformTemps() []tempReading {
	if !smcOpen() {
		return nil
	}
	smc.mu.Lock()
	defer smc.mu.Unlock()
	out := make([]tempReading, 0, len(smc.temps))
	same := map[float64]int{}
	for _, k := range smc.temps {
		v, ok := smcRead(k)
		if !ok || v < 10 || v >= 130 {
			continue
		}
		same[v]++
		out = append(out, tempReading{Key: k.name, Label: k.name, Kind: smcKind(k.name), C: v, Hidden: true})
	}
	// Unpopulated slots report a fixed placeholder (dozens of keys reading
	// exactly 40.0, say). Real sensors never agree to the last bit in bulk.
	live := out[:0]
	for _, t := range out {
		if same[t.C] < 6 {
			live = append(live, t)
		}
	}
	return live
}

func platformFans() []fanReading {
	if !smcOpen() {
		return nil
	}
	smc.mu.Lock()
	defer smc.mu.Unlock()
	var out []fanReading
	for i := 0; i < smc.fans; i++ {
		rpm, ok := smcReadName(fmt.Sprintf("F%dAc", i))
		if !ok {
			continue
		}
		max, _ := smcReadName(fmt.Sprintf("F%dMx", i))
		out = append(out, fanReading{Label: fmt.Sprintf("Fan %d", i+1), RPM: rpm, Max: max})
	}
	return out
}

func platformPower() (float64, bool) {
	if !smcOpen() || smc.power == nil {
		return 0, false
	}
	smc.mu.Lock()
	defer smc.mu.Unlock()
	return smcRead(*smc.power)
}
