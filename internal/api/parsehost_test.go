package api

import "testing"

func TestParseHost(t *testing.T) {
	cases := []struct {
		name     string
		host     string
		wantPort string
		wantID   string
		wantOK   bool
	}{
		{
			name:     "sdk_subdomain",
			host:     "49983-isb_abc.localhost",
			wantPort: "49983",
			wantID:   "isb_abc",
			wantOK:   true,
		},
		{
			name:     "sdk_subdomain_with_host_port",
			host:     "49983-isb_abc.localhost:3000",
			wantPort: "49983",
			wantID:   "isb_abc",
			wantOK:   true,
		},
		{
			name:     "ci_overlay_port",
			host:     "49999-isb_xyz.example.com",
			wantPort: "49999",
			wantID:   "isb_xyz",
			wantOK:   true,
		},
		{
			name:     "user_port",
			host:     "8080-isb_q.edvabe.test",
			wantPort: "8080",
			wantID:   "isb_q",
			wantOK:   true,
		},
		{name: "no_dot", host: "49983-isb_abc", wantOK: false},
		{name: "no_hyphen", host: "localhost:3000", wantOK: false},
		{name: "empty", host: "", wantOK: false},
		{name: "non_numeric_port", host: "foo-isb_abc.localhost", wantOK: false},
		{name: "empty_port", host: "-isb_abc.localhost", wantOK: false},
		{name: "empty_id", host: "49983-.localhost", wantOK: false},
		{name: "bare_loopback", host: "127.0.0.1:3000", wantOK: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			port, id, ok := parseHost(tc.host)
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v", ok, tc.wantOK)
			}
			if !tc.wantOK {
				return
			}
			if port != tc.wantPort {
				t.Errorf("port = %q, want %q", port, tc.wantPort)
			}
			if id != tc.wantID {
				t.Errorf("id = %q, want %q", id, tc.wantID)
			}
		})
	}
}
