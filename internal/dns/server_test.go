package dns

import (
	"encoding/binary"
	"net"
	"testing"
)

func buildQuery(id uint16, name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], id)
	binary.BigEndian.PutUint16(q[2:4], 0x0100) // RD
	binary.BigEndian.PutUint16(q[4:6], 1)      // qdcount
	for _, label := range splitLabels(name) {
		q = append(q, byte(len(label)))
		q = append(q, []byte(label)...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, qtype)
	q = binary.BigEndian.AppendUint16(q, 1) // IN
	return q
}

func splitLabels(name string) []string {
	var out []string
	start := 0
	for i := 0; i < len(name); i++ {
		if name[i] == '.' {
			if i > start {
				out = append(out, name[start:i])
			}
			start = i + 1
		}
	}
	if start < len(name) {
		out = append(out, name[start:])
	}
	return out
}

func TestHandleA_InDomain(t *testing.T) {
	q := buildQuery(0x1234, "3000-i1234abcd.sbx.edvabe", qtypeA)
	resp, name, qtype, rcode , _ := handle(q,"sbx.edvabe", net.ParseIP("172.28.0.2").To4())
	if resp == nil {
		t.Fatal("nil response")
	}
	if name != "3000-i1234abcd.sbx.edvabe" {
		t.Errorf("name = %q", name)
	}
	if qtype != qtypeA {
		t.Errorf("qtype = %d", qtype)
	}
	if rcode != rcodeNoError {
		t.Errorf("rcode = %d", rcode)
	}
	// last 4 bytes should be the IP
	ip := resp[len(resp)-4:]
	if !net.IP(ip).Equal(net.ParseIP("172.28.0.2")) {
		t.Errorf("ip = %v", net.IP(ip))
	}
	// ancount
	if ancount := binary.BigEndian.Uint16(resp[6:8]); ancount != 1 {
		t.Errorf("ancount = %d", ancount)
	}
}

func TestHandleA_OutOfDomain(t *testing.T) {
	q := buildQuery(0x5678, "google.com", qtypeA)
	resp, _, _, rcode , _ := handle(q,"sbx.edvabe", net.ParseIP("172.28.0.2").To4())
	if resp == nil {
		t.Fatal("nil response")
	}
	if rcode != rcodeRefused {
		t.Errorf("rcode = %d want REFUSED", rcode)
	}
	if ancount := binary.BigEndian.Uint16(resp[6:8]); ancount != 0 {
		t.Errorf("ancount = %d", ancount)
	}
}

func TestHandleAAAA_InDomain(t *testing.T) {
	q := buildQuery(0x9abc, "3000-i1234abcd.sbx.edvabe", qtypeAAAA)
	resp, _, _, rcode , _ := handle(q,"sbx.edvabe", net.ParseIP("172.28.0.2").To4())
	if resp == nil {
		t.Fatal("nil response")
	}
	if rcode != rcodeNoError {
		t.Errorf("rcode = %d", rcode)
	}
	if ancount := binary.BigEndian.Uint16(resp[6:8]); ancount != 0 {
		t.Errorf("ancount = %d want 0 (no AAAA)", ancount)
	}
}

func TestHandle_BaseDomainItself(t *testing.T) {
	q := buildQuery(1, "sbx.edvabe", qtypeA)
	resp, _, _, rcode , _ := handle(q,"sbx.edvabe", net.ParseIP("1.2.3.4").To4())
	if rcode != rcodeNoError {
		t.Errorf("rcode = %d", rcode)
	}
	if ancount := binary.BigEndian.Uint16(resp[6:8]); ancount != 1 {
		t.Errorf("ancount = %d", ancount)
	}
}
