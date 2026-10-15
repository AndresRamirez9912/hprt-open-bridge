//go:build unix

package cp4x

import (
	"net/netip"
	"testing"
)

// TestParseDiscoveryReply checks which UDP replies are printers
func TestParseDiscoveryReply(t *testing.T) {
	// Every reply comes from the address of the printer
	from := netip.MustParseAddr("192.168.2.152")

	testCases := []struct {
		name   string
		msg    string
		from   netip.Addr
		wantOK bool
		want   string
	}{
		{name: "printer reply", msg: "CP4100-7d87,CP410GL254801198", from: from, wantOK: true, want: "CP4100-7d87/CP410GL254801198"},
		{name: "reply with spaces", msg: " CP4100-16DD , SN1 ", from: from, wantOK: true, want: "CP4100-16DD/SN1"},
		{name: "name only", msg: "CP4100", from: from, wantOK: true, want: "CP4100/"},
		{name: "our own probe", msg: DiscoveryProbe, from: from},
		{name: "unspecified sender", msg: "CP4100,SN", from: netip.IPv4Unspecified()},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Decide whether the reply is a printer
			got, ok := parseDiscoveryReply([]byte(tc.msg), tc.from)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}

			// Name and serial number, trimmed
			if ok && got.Name+"/"+got.SN != tc.want {
				t.Errorf("got %s/%s, want %s", got.Name, got.SN, tc.want)
			}
		})
	}
}
