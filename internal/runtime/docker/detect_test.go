package docker

import "testing"

func TestParseContainerID(t *testing.T) {
	const id = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"host systemd service", "0::/system.slice/edvabe.service\n", ""},
		{"cgroup v1 docker", "12:memory:/docker/" + id + "\n", id},
		{"cgroup v2 systemd scope", "0::/system.slice/docker-" + id + ".scope\n", id},
		{"cgroup v2 compose", "0::/docker/" + id + "\n", id},
		{
			name: "mountinfo overlay path",
			in: "1234 1234 0:1 / /proc rw - proc proc rw\n" +
				"5678 5678 0:2 / /var/lib/docker/containers/" + id + "/hostname rw - ext4 /dev/xda rw\n",
			want: id,
		},
		{"short hex not matched", "0::/docker/abc123\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseContainerID(tc.in); got != tc.want {
				t.Errorf("parseContainerID = %q, want %q", got, tc.want)
			}
		})
	}
}
