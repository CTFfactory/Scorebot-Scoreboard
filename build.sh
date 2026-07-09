#!/usr/bin/bash
# Copyright (C) 2020 - 2023 iDigitalFlame
#
# This program is free software: you can redistribute it and/or modify
# it under the terms of the GNU Affero General Public License as published
# by the Free Software Foundation, either version 3 of the License, or
# any later version.
#
# This program is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
# GNU Affero General Public License for more details.
#
# You should have received a copy of the GNU Affero General Public License
# along with this program.  If not, see <https://www.gnu.org/licenses/>.
#

output="$(pwd)/bin/scoreboard"
if [ $# -ge 1 ]; then
    output="$1"
fi

mkdir -p "$(dirname "$output")"

printf "Building..\n"
version="$(date +%F)_$(git rev-parse --short HEAD 2> /dev/null || echo non-git)"
(
    cd scoreboard || exit 1
    go build -trimpath -buildvcs=false -ldflags "-s -w -X github.com/CTFfactory/Scorebot-Scoreboard/scoreboard.version=${version}" -o "$output" ./cmd/main.go
)

if command -v upx > /dev/null 2>&1 && [ -f "$output" ]; then
    upx --compress-exports=1 --strip-relocs=1 --compress-icons=2 --best --no-backup -9 "$output"
fi

printf "Done!\n"
