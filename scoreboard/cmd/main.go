// Copyright(C) 2020 - 2023 iDigitalFlame
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
//

package main

import (
	"flag"
	"os"

	"github.com/CTFfactory/Scorebot-Scoreboard/scoreboard"
)

var (
	cmdlineFunc = scoreboard.Cmdline
	runFunc     = func(s *scoreboard.Scoreboard) error { return s.Run() }
	stderrFunc  = func(msg string) { _, _ = os.Stderr.WriteString(msg) }
)

func run() int {
	s, err := cmdlineFunc()
	if err == flag.ErrHelp {
		return 2
	}
	if err != nil {
		stderrFunc("Error during startup: " + err.Error() + "!\n")
		return 1
	}
	if s == nil {
		return 0
	}
	if err := runFunc(s); err != nil {
		stderrFunc("Error during runtime: " + err.Error() + "!\n")
		return 1
	}
	return 0
}

func main() {
	os.Exit(run())
}
