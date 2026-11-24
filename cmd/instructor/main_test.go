package main

import (
	"reflect"
	"slices"
	"testing"

	"dbchecker/internal/report"
)

func recordWith(studentID, macs string) []string {
	rec := make([]string, len(report.Header))
	rec[0] = studentID
	rec[slices.Index(report.Header, "mac_addresses")] = macs
	return rec
}

func TestSharedDevices(t *testing.T) {
	sameMachine := "36:0c:4f:98:6b:40;52:ee:bb:79:88:cd;0a:00:27:00:00:00"
	records := [][]string{
		recordWith("A", sameMachine),
		recordWith("A", sameMachine), // 같은 학번의 여러 행은 한 번으로 센다
		recordWith("B", sameMachine),
		recordWith("C", "0a:00:27:00:00:00;7e:17:9d:fe:56:28"),                   // VirtualBox 고정 주소만 겹침 → 무시
		recordWith("D", "7e:17:9d:fe:56:28;ae:9d:2c:c6:55:fb;52:ee:bb:79:88:cd"), // A·B 와 1개, C 와 1개 겹침
	}
	got := sharedDevices(records)
	want := []devicePair{
		{A: "A", B: "B", Shared: 2, Fewer: 2},
		{A: "A", B: "D", Shared: 1, Fewer: 2},
		{A: "B", B: "D", Shared: 1, Fewer: 2},
		{A: "C", B: "D", Shared: 1, Fewer: 1},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v\nwant %+v", got, want)
	}
}

func TestSharedDevicesNoOverlap(t *testing.T) {
	records := [][]string{
		recordWith("A", "a4:83:e7:00:00:01"),
		recordWith("B", "a4:83:e7:00:00:02"),
		recordWith("C", ""),
	}
	if got := sharedDevices(records); len(got) != 0 {
		t.Errorf("겹침이 없어야 합니다: %+v", got)
	}
}
