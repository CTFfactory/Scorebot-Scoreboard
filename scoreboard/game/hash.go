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
	"errors"
	"sync"
	"unsafe"
)

const (
	fnvPrime = 1099511628211
	fnvStart = 14695981039346656037
)

var bufs = sync.Pool{
	New: func() interface{} {
		b := make([]byte, 8)
		return &b
	},
}

type hasher struct {
	h, s uint64
}
type stringer interface {
	String() string
}

func (h *hasher) Reset() {
	h.h, h.s = fnvStart, fnvStart
}
func (h hasher) Sum64() uint64 {
	return h.h
}
func (h *hasher) Write(b []byte) {
	if h.h == 0 {
		h.h = fnvStart
	}
	if h.s == 0 {
		h.s = fnvStart
	}
	h.s = updateFnv(h.s, b)
	h.h = updateFnv(h.h, b)
}
func (h *hasher) Segment() uint64 {
	v := h.s
	h.s = fnvStart
	return v
}
func updateFnv(h uint64, b []byte) uint64 {
	for i := range b {
		h *= fnvPrime
		h ^= uint64(b[i])
	}
	return h
}

func writeUint16Bytes(b []byte, n uint16) {
	b[0], b[1] = byte(n>>8), byte(n)
}

func writeUint32Bytes(b []byte, n uint32) {
	b[0], b[1] = byte(n>>24), byte(n>>16)
	b[2], b[3] = byte(n>>8), byte(n)
}

func writeUint64Bytes(b []byte, n uint64) {
	b[0], b[1] = byte(n>>56), byte(n>>48)
	b[2], b[3] = byte(n>>40), byte(n>>32)
	b[4], b[5] = byte(n>>24), byte(n>>16)
	b[6], b[7] = byte(n>>8), byte(n)
}

func (h *hasher) hashBool(v interface{}, b []byte) bool {
	i, ok := v.(bool)
	if !ok {
		return false
	}
	if i {
		b[0] = 1
	} else {
		b[0] = 0
	}
	h.Write(b[:1])
	return true
}

func (h *hasher) hashStringLike(v interface{}) bool {
	switch i := v.(type) {
	case []byte:
		h.Write(i)
	case string:
		h.Write([]byte(i))
	case stringer:
		h.Write([]byte(i.String()))
	default:
		return false
	}
	return true
}

func (h *hasher) hashFloat(v interface{}, b []byte) bool {
	switch i := v.(type) {
	case float32:
		writeUint32Bytes(b, *(*uint32)(unsafe.Pointer(&i)))
		h.Write(b[:4])
	case float64:
		writeUint64Bytes(b, *(*uint64)(unsafe.Pointer(&i)))
		h.Write(b)
	default:
		return false
	}
	return true
}

func (h *hasher) hashSmallInt(v interface{}, b []byte) bool {
	switch i := v.(type) {
	case int8:
		b[0] = byte(i)
		h.Write(b[:1])
	case uint8:
		b[0] = i
		h.Write(b[:1])
	case int16:
		writeUint16Bytes(b, uint16(i))
		h.Write(b[:2])
	case uint16:
		writeUint16Bytes(b, i)
		h.Write(b[:2])
	case int32:
		writeUint32Bytes(b, uint32(i))
		h.Write(b[:4])
	case uint32:
		writeUint32Bytes(b, i)
		h.Write(b[:4])
	default:
		return false
	}
	return true
}

func (h *hasher) hashLargeInt(v interface{}, b []byte) bool {
	switch i := v.(type) {
	case int64:
		writeUint64Bytes(b, uint64(i))
	case uint64:
		writeUint64Bytes(b, i)
	case int:
		writeUint64Bytes(b, uint64(i))
	case uint:
		writeUint64Bytes(b, uint64(i))
	default:
		return false
	}
	h.Write(b)
	return true
}

func (h *hasher) Hash(v interface{}) error {
	b := *bufs.Get().(*[]byte)
	defer bufs.Put(&b)
	_ = b[7]
	if h.hashBool(v, b) {
		return nil
	}
	if h.hashStringLike(v) {
		return nil
	}
	if h.hashFloat(v, b) {
		return nil
	}
	if h.hashSmallInt(v, b) {
		return nil
	}
	if h.hashLargeInt(v, b) {
		return nil
	}
	return errors.New("cannot hash the requested type")
}
