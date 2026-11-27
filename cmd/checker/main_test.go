package main

import "testing"

func TestInsideDir(t *testing.T) {
	cases := []struct {
		name, path, dir string
		want            bool
	}{
		{"임시 폴더 안", "/tmp/Temp1_checker.zip/checker", "/tmp", true},
		{"대소문자가 달라도 같은 폴더", "/TMP/x/checker", "/tmp", true},
		{"임시 폴더 밖", "/home/me/Downloads/checker", "/tmp", false},
		{"이름만 비슷한 형제 폴더", "/tmp2/checker", "/tmp", false},
		{"임시 폴더 경로가 비어 있음", "/tmp/checker", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := insideDir(c.path, c.dir)
			if got != c.want {
				t.Errorf("insideDir(%q, %q) = %v, want %v", c.path, c.dir, got, c.want)
			}
		})
	}
}
