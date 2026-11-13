package main

import "fmt"

// Virtual-key codes for the multimedia keys.
var mediaKeys = map[string]int{
	"mute": 0xAD, "voldown": 0xAE, "volup": 0xAF,
	"next": 0xB0, "prev": 0xB1, "playpause": 0xB3,
}

func mediaCmd(key string) []string {
	vk, ok := mediaKeys[key]
	if !ok {
		return nil
	}
	script := fmt.Sprintf(
		`$s='[DllImport("user32.dll")] public static extern void keybd_event(byte k, byte s, uint f, System.UIntPtr e);';`+
			`$t=Add-Type -MemberDefinition $s -Name K -Namespace PD -PassThru;`+
			`$t::keybd_event(%d,0,0,[System.UIntPtr]::Zero);$t::keybd_event(%d,0,2,[System.UIntPtr]::Zero)`, vk, vk)
	return []string{"powershell", "-NoProfile", "-NonInteractive", "-Command", script}
}

func openCmd(target string) []string {
	return []string{"rundll32", "url.dll,FileProtocolHandler", target}
}

func lockCmd() []string { return []string{"rundll32", "user32.dll,LockWorkStation"} }

func sessionEnv() []string { return nil }
