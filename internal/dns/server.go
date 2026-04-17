// Package dns implements a minimal UDP DNS server that answers A
// queries for <port>-<id>.<domain> hostnames with a configured IPv4
// address. It exists so E2B SDK apps running in sibling Docker
// containers can resolve sandbox preview URLs to edvabe itself without
// relying on host-side wildcard DNS (lvh.me, nip.io) that only works
// from the host.
//
// The server is intentionally single-purpose: it only answers A queries
// that match the configured base domain. Anything else gets REFUSED so
// misconfigured clients fail loud rather than silently black-holing
// unrelated lookups.
package dns

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// Server answers A queries for *.Domain with Answer. Queries for names
// outside Domain are forwarded to Upstream if set; otherwise REFUSED.
type Server struct {
	Addr     string // UDP listen address, e.g. ":53"
	Domain   string // base domain, e.g. "sbx.edvabe" — queried names must end with this
	Answer   net.IP // IPv4 to return in A records
	Upstream string // host:port to forward non-matching queries to (e.g. "127.0.0.11:53"); empty disables forwarding
	Logger   *slog.Logger
}

// ListenAndServe blocks until ctx is cancelled or a fatal error occurs.
func (s *Server) ListenAndServe(ctx context.Context) error {
	if s.Domain == "" {
		return errors.New("dns: Domain is required")
	}
	if ip4 := s.Answer.To4(); ip4 == nil {
		return fmt.Errorf("dns: Answer %v is not an IPv4 address", s.Answer)
	}
	log := s.Logger
	if log == nil {
		log = slog.Default()
	}

	pc, err := net.ListenPacket("udp", s.Addr)
	if err != nil {
		return fmt.Errorf("dns: listen %s: %w", s.Addr, err)
	}
	defer pc.Close()

	log.Info("dns server listening", "addr", s.Addr, "domain", s.Domain, "answer", s.Answer)

	go func() {
		<-ctx.Done()
		_ = pc.Close()
	}()

	base := strings.ToLower(strings.TrimSuffix(s.Domain, "."))
	ip4 := s.Answer.To4()

	buf := make([]byte, 1500)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			log.Warn("dns read", "err", err)
			continue
		}
		q := make([]byte, n)
		copy(q, buf[:n])
		go s.serveOne(pc, addr, q, base, ip4, log)
	}
}

func (s *Server) serveOne(pc net.PacketConn, addr net.Addr, q []byte, base string, ip4 net.IP, log *slog.Logger) {
	resp, qname, qtype, rcode, forward := handle(q, base, ip4)
	if forward && s.Upstream != "" {
		fwdResp, err := forwardQuery(s.Upstream, q)
		if err != nil {
			log.Warn("dns forward", "err", err, "name", qname, "upstream", s.Upstream)
			// fall through to REFUSED response from handle()
		} else {
			resp = fwdResp
		}
	}
	if resp == nil {
		return
	}
	if _, err := pc.WriteTo(resp, addr); err != nil {
		log.Warn("dns write", "err", err, "to", addr)
		return
	}
	log.Debug("dns answered", "name", qname, "qtype", qtype, "rcode", rcode, "client", addr, "forwarded", forward && s.Upstream != "")
}

func forwardQuery(upstream string, q []byte) ([]byte, error) {
	conn, err := net.Dial("udp", upstream)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, err
	}
	return buf[:n], nil
}

const (
	rcodeNoError  = 0
	rcodeFormErr  = 1
	rcodeNXDomain = 3
	rcodeRefused  = 5

	qtypeA    = 1
	qtypeAAAA = 28

	qclassIN = 1
)

// handle parses a DNS query and builds a response. Returns (nil, ..) on
// unparseable input (nothing to reply to). For out-of-domain queries it
// returns forward=true so the caller can forward to an upstream
// resolver; the returned resp is a pre-built REFUSED fallback used if
// forwarding fails or is disabled.
func handle(q []byte, base string, answer net.IP) (resp []byte, qname string, qtype uint16, rcode int, forward bool) {
	if len(q) < 12 {
		return nil, "", 0, 0, false
	}
	id := binary.BigEndian.Uint16(q[0:2])
	flags := binary.BigEndian.Uint16(q[2:4])
	qdcount := binary.BigEndian.Uint16(q[4:6])

	// only handle standard queries (OPCODE=0, QR=0)
	if flags&0x8000 != 0 {
		return nil, "", 0, 0, false
	}
	if qdcount != 1 {
		return buildHeader(id, rcodeFormErr, 0), "", 0, rcodeFormErr, false
	}

	name, qtype, qclass, _, err := parseQuestion(q[12:])
	if err != nil {
		return buildHeader(id, rcodeFormErr, 0), "", 0, rcodeFormErr, false
	}

	nameLower := strings.ToLower(strings.TrimSuffix(name, "."))

	inDomain := nameLower == base || strings.HasSuffix(nameLower, "."+base)
	if !inDomain {
		return buildResp(id, q[12:12+questionLen(q[12:])], rcodeRefused, nil), name, qtype, rcodeRefused, true
	}
	if qclass != qclassIN {
		return buildResp(id, q[12:12+questionLen(q[12:])], rcodeRefused, nil), name, qtype, rcodeRefused, false
	}

	// AAAA in the in-domain case: reply NOERROR with zero answers so
	// resolvers don't fall through / retry. This is standard behavior
	// for "name exists, type missing".
	if qtype == qtypeAAAA {
		return buildResp(id, q[12:12+questionLen(q[12:])], rcodeNoError, nil), name, qtype, rcodeNoError, false
	}
	if qtype != qtypeA {
		return buildResp(id, q[12:12+questionLen(q[12:])], rcodeNoError, nil), name, qtype, rcodeNoError, false
	}

	return buildResp(id, q[12:12+questionLen(q[12:])], rcodeNoError, answer.To4()), name, qtype, rcodeNoError, false
}

// parseQuestion reads one DNS question at the beginning of b.
func parseQuestion(b []byte) (name string, qtype, qclass uint16, consumed int, err error) {
	var labels []string
	i := 0
	for {
		if i >= len(b) {
			return "", 0, 0, 0, errors.New("truncated name")
		}
		l := int(b[i])
		if l == 0 {
			i++
			break
		}
		if l&0xc0 != 0 {
			// compression pointer — not expected in questions
			return "", 0, 0, 0, errors.New("compressed name in question")
		}
		i++
		if i+l > len(b) {
			return "", 0, 0, 0, errors.New("truncated label")
		}
		labels = append(labels, string(b[i:i+l]))
		i += l
	}
	if i+4 > len(b) {
		return "", 0, 0, 0, errors.New("truncated qtype/qclass")
	}
	qtype = binary.BigEndian.Uint16(b[i : i+2])
	qclass = binary.BigEndian.Uint16(b[i+2 : i+4])
	return strings.Join(labels, "."), qtype, qclass, i + 4, nil
}

// questionLen returns the number of bytes the first question occupies,
// or 0 if it can't be parsed. Used to echo the question verbatim in the
// response.
func questionLen(b []byte) int {
	_, _, _, n, err := parseQuestion(b)
	if err != nil {
		return 0
	}
	return n
}

func buildHeader(id uint16, rcode int, ancount uint16) []byte {
	resp := make([]byte, 12)
	binary.BigEndian.PutUint16(resp[0:2], id)
	// QR=1, OPCODE=0, AA=1, TC=0, RD=0, RA=0, Z=0, RCODE=rcode
	flags := uint16(0x8400) | uint16(rcode&0xF)
	binary.BigEndian.PutUint16(resp[2:4], flags)
	binary.BigEndian.PutUint16(resp[6:8], ancount)
	return resp
}

// buildResp builds a response echoing the question and (optionally)
// appending a single A-record answer.
func buildResp(id uint16, question []byte, rcode int, ip4 net.IP) []byte {
	var ancount uint16
	if ip4 != nil && len(ip4) == 4 {
		ancount = 1
	}
	resp := buildHeader(id, rcode, ancount)
	// qdcount = 1 (we echo the question)
	binary.BigEndian.PutUint16(resp[4:6], 1)
	resp = append(resp, question...)
	if ancount == 1 {
		// pointer to the question name (offset 12)
		ans := []byte{0xc0, 0x0c}
		// TYPE=A, CLASS=IN, TTL=60, RDLENGTH=4, RDATA=ip4
		ans = binary.BigEndian.AppendUint16(ans, qtypeA)
		ans = binary.BigEndian.AppendUint16(ans, qclassIN)
		ans = binary.BigEndian.AppendUint32(ans, 60)
		ans = binary.BigEndian.AppendUint16(ans, 4)
		ans = append(ans, ip4...)
		resp = append(resp, ans...)
	}
	return resp
}
