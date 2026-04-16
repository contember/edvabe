package docker

import (
	"regexp"
	"testing"
)

func TestDetectOwnContainerIDParsing(t *testing.T) {
	const id = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
	const overlayID = "fedcba9876543210fedcba9876543210fedcba9876543210fedcba9876543210"

	t.Run("cgroup v1 docker", func(t *testing.T) {
		assertMatch(t, cgroupContainerIDPattern, "12:memory:/docker/"+id+"\n", id)
	})
	t.Run("cgroup v2 systemd scope", func(t *testing.T) {
		assertMatch(t, cgroupContainerIDPattern, "0::/system.slice/docker-"+id+".scope\n", id)
	})
	t.Run("cgroup v2 private namespace", func(t *testing.T) {
		// With cgroupns=private (modern Docker default) cgroup is "0::/".
		assertMatch(t, cgroupContainerIDPattern, "0::/\n", "")
	})
	t.Run("host systemd service", func(t *testing.T) {
		assertMatch(t, cgroupContainerIDPattern, "0::/system.slice/edvabe.service\n", "")
	})

	t.Run("mountinfo with overlay path first", func(t *testing.T) {
		// Real-world mountinfo inside a Compose container: overlay2
		// snapshot hash (also 64-hex) appears BEFORE /containers/<id>/
		// — we must not match the overlay hash.
		in := "15082 6748 0:154 / / rw - overlay overlay rw,lowerdir=/var/lib/docker/overlay2/" + overlayID + "/diff\n" +
			"15093 15082 252:1 /var/lib/docker/containers/" + id + "/resolv.conf /etc/resolv.conf rw - ext4\n"
		m := mountinfoContainerIDPattern.FindStringSubmatch(in)
		if len(m) != 2 || m[1] != id {
			t.Errorf("got %v, want capture %q", m, id)
		}
	})
	t.Run("mountinfo without container path", func(t *testing.T) {
		in := "1234 1234 0:1 / /proc rw - proc proc rw\n"
		if m := mountinfoContainerIDPattern.FindStringSubmatch(in); m != nil {
			t.Errorf("unexpected match: %v", m)
		}
	})
}

func assertMatch(t *testing.T, re *regexp.Regexp, in, want string) {
	t.Helper()
	got := re.FindString(in)
	if got != want {
		t.Errorf("FindString(%q) = %q, want %q", in, got, want)
	}
}
