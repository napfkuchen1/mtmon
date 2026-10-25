package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// Ookla runs the official `speedtest` CLI if it is installed. mtmon does NOT ship or download it: Ookla's licence
// forbids redistribution and the user has to accept it (the setting "ookla_accepted" records that decision).
type Ookla struct {
	DataDir  string // <DataDir>/ookla/speedtest is looked at before PATH
	ServerID int    // 0 = automatic
	RunCmd   func(ctx context.Context, bin string, args []string) ([]byte, error)
}

func (Ookla) Name() string { return "ookla" }

// binary returns the CLI path, "" when it is not installed.
func (p Ookla) binary() string {
	if p.DataDir != "" {
		b := filepath.Join(p.DataDir, "ookla", "speedtest")
		if st, err := os.Stat(b); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return b
		}
	}
	if b, err := exec.LookPath("speedtest"); err == nil {
		return b
	}
	return ""
}

func (p Ookla) Run(ctx context.Context, o Opts) Result {
	res := Result{Server: "ookla"}
	bin := p.binary()
	if bin == "" && p.RunCmd == nil {
		res.Err = "Ookla speedtest CLI not installed (put it in <data dir>/ookla/speedtest or into PATH)"
		return res
	}
	args := []string{"--accept-license", "--accept-gdpr", "--format=json"}
	if p.ServerID > 0 {
		args = append(args, "--server-id="+strconv.Itoa(p.ServerID))
	}
	// the CLI sizes the test itself; this only bounds how long it may run
	ctx, cancel := context.WithTimeout(ctx, 3*o.MaxDur+30*time.Second)
	defer cancel()
	start := time.Now()
	run := p.RunCmd
	if run == nil {
		run = func(ctx context.Context, bin string, args []string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).Output()
		}
	}
	out, err := run(ctx, bin, args)
	res.Dur = time.Since(start)
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) > 0 {
			res.Err = trimErr(fmt.Errorf("%s", string(ee.Stderr)))
		} else {
			res.Err = trimErr(err)
		}
		return res
	}
	return parseOokla(out, res)
}

// parseOokla reads the CLI's JSON (bandwidth is bytes per second).
func parseOokla(out []byte, res Result) Result {
	var j struct {
		Ping struct {
			Latency float64 `json:"latency"`
			Jitter  float64 `json:"jitter"`
		} `json:"ping"`
		Download struct {
			Bandwidth float64 `json:"bandwidth"`
			Bytes     int64   `json:"bytes"`
		} `json:"download"`
		Upload struct {
			Bandwidth float64 `json:"bandwidth"`
			Bytes     int64   `json:"bytes"`
		} `json:"upload"`
		Server struct {
			Host string `json:"host"`
			Name string `json:"name"`
		} `json:"server"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(out, &j); err != nil {
		res.Err = "unreadable answer from the Ookla CLI"
		return res
	}
	if j.Error != "" {
		res.Err = trimErr(errors.New(j.Error))
		return res
	}
	if j.Download.Bandwidth <= 0 {
		res.Err = "the Ookla CLI reported no download speed"
		return res
	}
	res.DownBps, res.UpBps = j.Download.Bandwidth*8, j.Upload.Bandwidth*8
	res.PingMS, res.JitterMS = j.Ping.Latency, j.Ping.Jitter
	res.Bytes = j.Download.Bytes + j.Upload.Bytes
	if j.Server.Host != "" {
		res.Server = j.Server.Host
	}
	return res
}
