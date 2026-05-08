package web

import "testing"

func TestNormaliseVolumeHostPath(t *testing.T) {
	stack := "/opt/stacks/myproj"
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"data", "data"},
		{"./data", "data"},
		{"data/.", "data"},
		{"data/sub/..", "data"},
		{"./data/./logs", "data/logs"},
		{"/opt/stacks/myproj/data", "data"},
		{"/opt/stacks/myproj/data/", "data"},
		{"/opt/stacks/myproj/", "/opt/stacks/myproj"}, // exact match → keep absolute (rel=".")
		{"/var/log/myapp", "/var/log/myapp"},          // outside stack → keep
		{"/opt/stacks/otherproj/data", "/opt/stacks/otherproj/data"},
	}
	for _, c := range cases {
		got := normaliseVolumeHostPath(c.in, stack)
		if got != c.want {
			t.Errorf("normaliseVolumeHostPath(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// Real-world: non-default stack root under /mnt/nvme.
	custom := "/mnt/nvme/bbsit/stacks/company-site-scraper"
	if got := normaliseVolumeHostPath("/mnt/nvme/bbsit/stacks/company-site-scraper/data", custom); got != "data" {
		t.Errorf("nvme case: got %q, want %q", got, "data")
	}
}
