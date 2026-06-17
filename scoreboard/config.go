// Copyright(C) 2020 - 2026 iDigitalFlame
// Copyright(C) 2026 luftegrof
//
// This program is free software: you can redistribute it and / or modify
// it under the terms of the GNU Affero General Public License as published
// by the Free Software Foundation, either version 3 of the License, or
// any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.If not, see <https://www.gnu.org/licenses/>.

package scoreboard

import (
	"encoding/json"
	"flag"
	"log/slog"
	"os"
)

var version = "unknown"

const defaults = `{
    "tick": 5,
    "assets": "",
    "listen": "0.0.0.0:8080",
    "timeout": 10,
    "scorebot": "http://scorebot"
}
`
const usage = `Scorebot Scoreboard v2.5 (Lite)

Usage of scoreboard:
  -c <file>                 Scorebot configuration file path.
  -d                        Print default configuration and exit.
  -V                        Print version string and exit.
  -sbe <url>                Scorebot core address or URL (Required without "-c").
  -assets <dir>             Scoreboard secondary assets override URL.
  -dir <directory>          Scoreboard HTML override directory path.
  -tick <seconds>           Scorebot poll rate, in seconds (Default 5).
  -timeout <seconds>        Scoreboard request timeout, in seconds (Default 10).
  -bind <socket>            Address and port to listen on (Default "0.0.0.0:8080").
  -cert <file>              Path to TLS certificate file.
  -key <file>               Path to TLS key file.
`

type config struct {
	Scorebot  string `json:"scorebot"`
	Key       string `json:"key,omitempty"`
	Cert      string `json:"cert,omitempty"`
	Directory string `json:"dir,omitempty"`
	Assets    string `json:"assets"`
	Listen    string `json:"listen"`
	Timeout   int    `json:"timeout"`
	Tick      int    `json:"tick"`
}

func newFlags(c *config) (*flag.FlagSet, *bool, *bool, *string) {
	var (
		args = flag.NewFlagSet("Scorebot Scoreboard", flag.ExitOnError)
		d    bool
		V    bool
		s    string
	)
	args.Usage = func() {
		os.Stdout.WriteString(usage)
		os.Exit(2)
	}
	args.StringVar(&s, "c", "", "")
	args.BoolVar(&d, "d", false, "")
	args.BoolVar(&V, "V", false, "")
	args.StringVar(&c.Scorebot, "sbe", "", "")
	args.StringVar(&c.Assets, "assets", "", "")
	args.StringVar(&c.Directory, "dir", "", "")
	args.IntVar(&c.Tick, "tick", 5, "")
	args.IntVar(&c.Timeout, "timeout", 10, "")
	args.StringVar(&c.Listen, "bind", "0.0.0.0:8080", "")
	args.StringVar(&c.Key, "key", "", "")
	args.StringVar(&c.Cert, "cert", "", "")
	return args, &d, &V, &s
}

func parseFlags(args *flag.FlagSet) error {
	if err := args.Parse(os.Args[1:]); err != nil {
		os.Stdout.WriteString(usage)
		return flag.ErrHelp
	}
	return nil
}

func handleCmdlineOutput(V, d bool) (*Scoreboard, bool) {
	if V {
		os.Stdout.WriteString("Scorebot Scoreboard: " + version + "\n")
		return nil, true
	}
	if d {
		os.Stdout.WriteString(defaults)
		return nil, true
	}
	return nil, false
}

func loadConfigFile(c *config, file string) error {
	if len(file) == 0 {
		return nil
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, c)
}

func initLogger() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)
}

func (c *config) verify() error {
	if c.Tick <= 0 {
		c.Tick = 5
	}
	if c.Timeout <= 0 {
		c.Timeout = 10
	}
	if len(c.Listen) == 0 {
		c.Listen = "0.0.0.0:8080"
	}
	return nil
}

func Cmdline() (*Scoreboard, error) {
	var c config
	args, d, V, s := newFlags(&c)
	if err := parseFlags(args); err != nil {
		return nil, err
	}
	if _, ok := handleCmdlineOutput(*V, *d); ok {
		return nil, nil
	}
	if len(*s) == 0 && len(c.Scorebot) == 0 {
		os.Stdout.WriteString(usage)
		return nil, flag.ErrHelp
	}
	if err := loadConfigFile(&c, *s); err != nil {
		return nil, err
	}
	_ = c.verify()
	initLogger()
	return c.New()
}
