package web

import "testing"

func TestIsValidID(t *testing.T) {
	valid := []string{
		"abc",
		"a",
		"123",
		"abc-def",
		"my-project",
		"abc123",
		"a1b2c3",
		"proj-v2",
		"x-y-z",
	}
	for _, id := range valid {
		if !isValidID(id) {
			t.Errorf("isValidID(%q) = false, want true", id)
		}
	}

	invalid := []string{
		"",
		"-abc",
		"abc-",
		"-",
		"ABC",
		"Abc",
		"abc_def",
		"abc def",
		"abc.def",
		"abc/def",
	}
	for _, id := range invalid {
		if isValidID(id) {
			t.Errorf("isValidID(%q) = true, want false", id)
		}
	}
}
