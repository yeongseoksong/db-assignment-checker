package main

import "testing"

func TestUseCLI(t *testing.T) {
	cases := []struct {
		name    string
		cliFlag bool
		given   []string
		want    bool
	}{
		{"플래그 없음(더블클릭)은 창 모드", false, nil, false},
		{"창 모드 전용 플래그만", false, []string{"no-browser", "out"}, false},
		{"-cli", true, nil, true},
		{"-port 없이 입력 플래그만", false, []string{"id", "db", "db-user"}, true},
		{"입력 플래그 하나만", false, []string{"id"}, true},
		{"-port 만", false, []string{"port"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			given := map[string]bool{}
			for _, name := range c.given {
				given[name] = true
			}
			got := useCLI(c.cliFlag, given)
			if got != c.want {
				t.Errorf("got %v want %v", got, c.want)
			}
		})
	}
}
