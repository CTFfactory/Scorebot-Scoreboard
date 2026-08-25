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

package game

import (
	"encoding/json"
	"strconv"
	"strings"
)

const (
	red    state = 0x2
	green  state = 0x0
	yellow state = 0x1

	tcp  protocol = 0x0
	udp  protocol = 0x1
	icmp protocol = 0x2
)

var (
	emptyHost    host
	emptyService service
)

type state uint8
type host struct {
	Name     string    `json:"name"`
	Services []service `json:"services"`
	ID       uint64    `json:"id"`
	hash     uint64
	total    uint64
	Online   bool `json:"online"`
}
type protocol uint8
type service struct {
	ID       uint64   `json:"id"`
	Port     uint16   `json:"port"`
	State    state    `json:"status"`
	Bonus    bool     `json:"bool"`
	Protocol protocol `json:"protocol"`

	hash uint64
}

func (h host) Sum() uint64 {
	return h.ID
}
func (s service) Sum() uint64 {
	return s.ID
}
func (s state) class() string {
	switch s {
	case red:
		return "err"
	case yellow:
		return "warn"
	case green:
		return "port"
	}
	return "port"
}
func (s state) String() string {
	switch s {
	case red:
		return "rgb(255, 0, 0)"
	case yellow:
		return "rgb(173, 164, 21)"
	case green:
		return "rgb(40, 111, 36)"
	}
	return "rgb(255, 0, 0)"
}
func (p protocol) String() string {
	switch p {
	case tcp:
		return "tcp"
	case udp:
		return "udp"
	case icmp:
		return "icmp"
	}
	return "Unknown"
}
func (h *host) Hash(i *hasher) uint64 {
	if h.hash == 0 {
		_ = i.Hash(h.ID)
		_ = i.Hash(h.Name)
		_ = i.Hash(h.Online)
		h.hash = i.Segment()
	}
	h.total = h.hash
	for s := range h.Services {
		h.total += h.Services[s].Hash(i)
	}
	return h.hash
}
func (s *service) Hash(h *hasher) uint64 {
	if s.hash == 0 {
		_ = h.Hash(s.ID)
		_ = h.Hash(s.Port)
		_ = h.Hash(s.State)
		_ = h.Hash(s.Bonus)
		_ = h.Hash(s.Protocol)
		s.hash = h.Segment()
	}
	return s.hash
}

func (h host) writeHeader(p *planner, existing bool) {
	id := "host-h" + strconv.FormatUint(h.ID, 10)
	if existing {
		p.Value(id, "", "host")
		return
	}
	p.DeltaValue(id, "", "host")
}

func (h host) writeStateValue(p *planner) {
	if h.Online {
		p.Property("", "-offline", "class")
		return
	}
	p.Property("", "+offline", "class")
}

func (h host) writeStateDelta(p *planner) {
	if h.Online {
		p.DeltaProperty("", "-offline", "class")
		return
	}
	p.DeltaProperty("", "+offline", "class")
}

func (h host) compareServicesByIndex(p *planner, o host) {
	for i := range h.Services {
		h.Services[i].Compare(p, o.Services[i])
	}
}

func (h host) compareServicesByMap(p *planner, o host) {
	c := make(compare)
	for i := range o.Services {
		c.One(o.Services[i])
	}
	for i := range h.Services {
		c.Two(h.Services[i])
	}
	for k, v := range c {
		switch {
		case !v.Second():
			p.Remove("s" + strconv.FormatUint(k, 10))
		case !v.First():
			v.B.(service).Compare(p, emptyService)
		default:
			v.B.(service).Compare(p, v.A.(service))
		}
	}
}

func (h host) Compare(p *planner, o host) {
	h.writeHeader(p, o.ID > 0)
	p.Prefix(p.prefix + "-host-h" + strconv.FormatUint(h.ID, 10))
	if o.hash == h.hash {
		p.Value("name", h.Name, "host-name")
		h.writeStateValue(p)
		if o.total == h.total {
			h.compareServicesByIndex(p, o)
		}
	} else {
		p.DeltaValue("name", h.Name, "host-name")
		h.writeStateDelta(p)
	}
	if o.ID == 0 || o.total != h.total {
		h.compareServicesByMap(p, o)
	}
	p.rollbackPrefix()
}
func (s *state) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch strings.ToLower(v) {
	case "red", "r", "fail":
		*s = red
	case "yellow", "y", "issue":
		*s = yellow
	case "green", "g", "good", "ok":
		*s = green
	default:
		*s = red
	}
	return nil
}
func (s service) Compare(p *planner, o service) {
	if o.ID == 0 {
		p.DeltaValue("s"+strconv.FormatUint(s.ID, 10), "", "service")
	} else {
		p.Value("s"+strconv.FormatUint(s.ID, 10), "", "service")
	}
	p.Prefix(p.prefix + "-s" + strconv.FormatUint(s.ID, 10))
	if o.hash == s.hash {
		p.Value("port", s.Port, s.State.class())
		p.Value("protocol", s.Protocol.String(), "service-protocol")
		if s.Bonus {
			p.Property("", "+bonus", "class")
		} else {
			p.Property("", "-bonus", "class")
		}
		p.Property("", s.State.String(), "background-color")
		p.rollbackPrefix()
		return
	}
	p.DeltaValue("port", s.Port, s.State.class())
	p.DeltaValue("protocol", s.Protocol.String(), "service-protocol")
	if s.Bonus {
		p.DeltaProperty("", "+bonus", "class")
	} else {
		p.DeltaProperty("", "-bonus", "class")
	}
	p.DeltaProperty("", s.State.String(), "background-color")
	p.rollbackPrefix()
}
func (p *protocol) UnmarshalJSON(b []byte) error {
	var v string
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch strings.ToLower(v) {
	case "tcp", "t":
		*p = tcp
	case "udp", "u":
		*p = udp
	case "icmp", "i", "p", "ping":
		*p = icmp
	default:
		*p = tcp
	}
	return nil
}
