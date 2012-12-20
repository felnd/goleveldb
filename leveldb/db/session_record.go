// Copyright (c) 2012, Suryandaru Triandana <syndtr@gmail.com>
// All rights reserved.
//
// Use of this source code is governed by a BSD-style license that can be
// found in the LICENSE file.

// This LevelDB Go implementation is based on LevelDB C++ implementation.
// Which contains the following header:
//   Copyright (c) 2011 The LevelDB Authors. All rights reserved.
//   Use of this source code is governed by a BSD-style license that can be
//   found in the LEVELDBCPP_LICENSE file. See the LEVELDBCPP_AUTHORS file
//   for names of contributors.

package db

import (
	"bytes"
	"encoding/binary"
	"io"
	"leveldb"
)

// These numbers are written to disk and should not be changed.
const (
	_ uint64 = iota
	tagComparator
	tagLogNum
	tagNextNum
	tagSequence
	tagCompactPointer
	tagDeletedTable
	tagNewTable
	// 8 was used for large value refs
	_
	tagPrevLogNum
)

const tagMax = tagPrevLogNum

var tagBytesCache [tagMax + 1][]byte

func init() {
	tmp := make([]byte, binary.MaxVarintLen32)
	for i := range tagBytesCache {
		n := binary.PutUvarint(tmp, uint64(i))
		b := make([]byte, n)
		copy(b, tmp)
		tagBytesCache[i] = b
	}
}

type cpRecord struct {
	level int
	key   iKey
}

type ntRecord struct {
	level    int
	num      uint64
	size     uint64
	smallest iKey
	largest  iKey
}

func (r ntRecord) makeFile(s *session) *tFile {
	return newTFile(s.getTableFile(r.num), r.size, r.smallest, r.largest)
}

type dtRecord struct {
	level int
	num   uint64
}

type sessionRecord struct {
	hasComparator bool
	comparator    string

	hasLogNum bool
	logNum    uint64

	hasNextNum bool
	nextNum    uint64

	hasSequence bool
	sequence    uint64

	compactPointers []cpRecord
	newTables       []ntRecord
	deletedTables   []dtRecord
}

func (p *sessionRecord) setComparator(name string) {
	p.hasComparator = true
	p.comparator = name
}

func (p *sessionRecord) setLogNum(num uint64) {
	p.hasLogNum = true
	p.logNum = num
}

func (p *sessionRecord) setNextNum(num uint64) {
	p.hasNextNum = true
	p.nextNum = num
}

func (p *sessionRecord) setSequence(seq uint64) {
	p.hasSequence = true
	p.sequence = seq
}

func (p *sessionRecord) addCompactPointer(level int, key iKey) {
	p.compactPointers = append(p.compactPointers, cpRecord{level, key})
}

func (p *sessionRecord) addTable(level int, num, size uint64, smallest, largest iKey) {
	p.newTables = append(p.newTables, ntRecord{level, num, size, smallest, largest})
}

func (p *sessionRecord) addTableFile(level int, t *tFile) {
	p.addTable(level, t.file.Number(), t.size, t.smallest, t.largest)
}

func (p *sessionRecord) deleteTable(level int, num uint64) {
	p.deletedTables = append(p.deletedTables, dtRecord{level, num})
}

func (p *sessionRecord) encodeTo(w io.Writer) (err error) {
	tmp := make([]byte, binary.MaxVarintLen64)

	putUvarint := func(p uint64) (err error) {
		n := binary.PutUvarint(tmp, p)
		_, err = w.Write(tmp[:n])
		return
	}

	putBytes := func(p []byte) (err error) {
		err = putUvarint(uint64(len(p)))
		if err != nil {
			return
		}
		_, err = w.Write(p)
		if err != nil {
			return
		}
		return
	}

	if p.hasComparator {
		_, err = w.Write(tagBytesCache[tagComparator])
		if err != nil {
			return
		}
		err = putBytes([]byte(p.comparator))
		if err != nil {
			return
		}
	}

	if p.hasLogNum {
		_, err = w.Write(tagBytesCache[tagLogNum])
		if err != nil {
			return
		}
		err = putUvarint(p.logNum)
		if err != nil {
			return
		}
	}

	if p.hasNextNum {
		_, err = w.Write(tagBytesCache[tagNextNum])
		if err != nil {
			return
		}
		err = putUvarint(p.nextNum)
		if err != nil {
			return
		}
	}

	if p.hasSequence {
		_, err = w.Write(tagBytesCache[tagSequence])
		if err != nil {
			return
		}
		err = putUvarint(uint64(p.sequence))
		if err != nil {
			return
		}
	}

	for _, p := range p.compactPointers {
		_, err = w.Write(tagBytesCache[tagCompactPointer])
		if err != nil {
			return
		}
		err = putUvarint(uint64(p.level))
		if err != nil {
			return
		}
		err = putBytes(p.key)
		if err != nil {
			return
		}
	}

	for _, p := range p.deletedTables {
		_, err = w.Write(tagBytesCache[tagDeletedTable])
		if err != nil {
			return
		}
		err = putUvarint(uint64(p.level))
		if err != nil {
			return
		}
		err = putUvarint(p.num)
		if err != nil {
			return
		}
	}

	for _, p := range p.newTables {
		_, err = w.Write(tagBytesCache[tagNewTable])
		if err != nil {
			return
		}
		err = putUvarint(uint64(p.level))
		if err != nil {
			return
		}
		err = putUvarint(p.num)
		if err != nil {
			return
		}
		err = putUvarint(p.size)
		if err != nil {
			return
		}
		err = putBytes(p.smallest)
		if err != nil {
			return
		}
		err = putBytes(p.largest)
		if err != nil {
			return
		}
	}

	return
}

func (p *sessionRecord) encode() []byte {
	b := new(bytes.Buffer)
	p.encodeTo(b)
	return b.Bytes()
}

func (p *sessionRecord) decodeFrom(r readByteReader) (err error) {
	for err == nil {
		var tag uint64
		tag, err = binary.ReadUvarint(r)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return
		}

		switch tag {
		case tagComparator:
			var cmp []byte
			cmp, err = readBytes(r)
			if err == nil {
				p.comparator = string(cmp)
				p.hasComparator = true
			}
		case tagLogNum:
			p.logNum, err = binary.ReadUvarint(r)
			if err == nil {
				p.hasLogNum = true
			}
		case tagPrevLogNum:
			err = leveldb.ErrInvalid("unsupported db format")
			break
		case tagNextNum:
			p.nextNum, err = binary.ReadUvarint(r)
			if err == nil {
				p.hasNextNum = true
			}
		case tagSequence:
			var seq uint64
			seq, err = binary.ReadUvarint(r)
			if err == nil {
				p.sequence = seq
				p.hasSequence = true
			}
		case tagCompactPointer:
			var level uint64
			var b []byte
			level, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			b, err = readBytes(r)
			if err != nil {
				break
			}
			p.addCompactPointer(int(level), b)
		case tagNewTable:
			var level, num, size uint64
			var b []byte
			level, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			num, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			size, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			b, err = readBytes(r)
			if err != nil {
				break
			}
			smallest := iKey(b)
			b, err = readBytes(r)
			if err != nil {
				break
			}
			largest := iKey(b)
			p.addTable(int(level), num, size, smallest, largest)
		case tagDeletedTable:
			var level, num uint64
			level, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			num, err = binary.ReadUvarint(r)
			if err != nil {
				break
			}
			p.deleteTable(int(level), num)
		}
	}

	return
}

func (p *sessionRecord) decode(buf []byte) error {
	b := bytes.NewBuffer(buf)
	return p.decodeFrom(b)
}
