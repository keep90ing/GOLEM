// cmd/ctl/main.go
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
)

type cfg struct {
	SampleRate   uint32 `json:"sample_rate,omitempty"`
	EnableFilter uint32 `json:"enable_filter,omitempty"`
	TargetLevel  uint32 `json:"target_level,omitempty"`
	TargetCgid   uint64 `json:"target_cgid,omitempty"`
}

func main() {
	addr := flag.String("addr", "http://127.0.0.1:2112", "exporter base URL")
	sample := flag.Uint("sample", 0, "sample rate (>0)")
	enable := flag.Bool("filter", true, "enable subtree filter")
	level  := flag.Uint("level", 0, "target level")
	cgid   := flag.Uint64("cgid", 0, "target cgroup inode id")
	flag.Parse()

	req := cfg{
		SampleRate:   uint32(*sample),
		EnableFilter: map[bool]uint32{true:1,false:0}[*enable],
		TargetLevel:  uint32(*level),
		TargetCgid:   *cgid,
	}
	body, _ := json.Marshal(req)
	resp, err := http.Post(*addr+"/admin/config", "application/json", bytes.NewReader(body))
	if err != nil { panic(err) }
	defer resp.Body.Close()
	fmt.Println("status:", resp.Status)
}
