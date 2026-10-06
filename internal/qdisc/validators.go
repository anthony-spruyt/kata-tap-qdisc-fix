package qdisc

import "regexp"

var kataTapDeviceRE = regexp.MustCompile(`^tap[0-9]+_kata$`)

// IsKataTapDevice reports whether ifname is a Kata cloud-hypervisor tap (tap<N>_kata).
func IsKataTapDevice(ifname string) bool {
	return kataTapDeviceRE.MatchString(ifname)
}
