package main

// Minimal wrapper over Windows Performance Data Helper (PDH), the API behind
// Task Manager and Performance Monitor. It needs no admin rights and no
// third-party driver.

import (
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modPdh               = syscall.NewLazyDLL("pdh.dll")
	procPdhOpenQuery     = modPdh.NewProc("PdhOpenQueryW")
	procPdhAddEnglish    = modPdh.NewProc("PdhAddEnglishCounterW")
	procPdhCollect       = modPdh.NewProc("PdhCollectQueryData")
	procPdhGetArray      = modPdh.NewProc("PdhGetFormattedCounterArrayW")
	pdhMoreData          = uintptr(0x800007D2)
	pdhFmtDoubleNoCap100 = uintptr(0x00000200 | 0x00008000)
)

type pdhQuery struct {
	handle   uintptr
	counters map[string]uintptr
}

// newPdhQuery opens a query for the given counter paths (English names, with
// "*" for all instances). Paths that do not exist on this machine are skipped.
func newPdhQuery(paths ...string) *pdhQuery {
	q := &pdhQuery{counters: map[string]uintptr{}}
	if r, _, _ := procPdhOpenQuery.Call(0, 0, uintptr(unsafe.Pointer(&q.handle))); r != 0 {
		return nil
	}
	for _, p := range paths {
		ptr, err := syscall.UTF16PtrFromString(p)
		if err != nil {
			continue
		}
		var h uintptr
		if r, _, _ := procPdhAddEnglish.Call(q.handle, uintptr(unsafe.Pointer(ptr)), 0, uintptr(unsafe.Pointer(&h))); r == 0 {
			q.counters[p] = h
		}
	}
	q.collect() // rate counters need a first sample to measure from
	return q
}

func (q *pdhQuery) has(path string) bool { _, ok := q.counters[path]; return ok }

func (q *pdhQuery) collect() bool {
	r, _, _ := procPdhCollect.Call(q.handle)
	return r == 0
}

// pdhItem mirrors PDH_FMT_COUNTERVALUE_ITEM_W for the double format.
type pdhItem struct {
	name   *uint16
	status uint32
	_      uint32
	value  float64
}

// values returns instance name -> value for a wildcard counter, from the most
// recent collect().
func (q *pdhQuery) values(path string) map[string]float64 {
	h, ok := q.counters[path]
	if !ok {
		return nil
	}
	var size, count uint32
	r, _, _ := procPdhGetArray.Call(h, pdhFmtDoubleNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), 0)
	if r != pdhMoreData || size == 0 {
		return nil
	}
	// The item array and the instance names it points to share one buffer;
	// allocate it as pdhItem so the pointers inside stay properly aligned.
	buf := make([]pdhItem, size/uint32(unsafe.Sizeof(pdhItem{}))+1)
	r, _, _ = procPdhGetArray.Call(h, pdhFmtDoubleNoCap100, uintptr(unsafe.Pointer(&size)), uintptr(unsafe.Pointer(&count)), uintptr(unsafe.Pointer(&buf[0])))
	if r != 0 {
		return nil
	}
	out := make(map[string]float64, count)
	for i := uint32(0); i < count && int(i) < len(buf); i++ {
		if buf[i].status != 0 && buf[i].status != 1 { // PDH_CSTATUS_VALID_DATA / NEW_DATA
			continue
		}
		out[windows.UTF16PtrToString(buf[i].name)] = buf[i].value
	}
	return out
}
