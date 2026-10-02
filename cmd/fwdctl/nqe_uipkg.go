package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
)

// The package Forward's NQE library Export/Import uses (NqeLibExport.java, NqeLibProtos.proto): a zip holding ONE entry, queries-export.proto, which is a binary protobuf
//
//	message NqeLibExportPB    { repeated ExportedQueryPB queries = 1; }
//	message ExportedQueryPB   { QueryPathPB path = 1; NqeQuerySourcePB source_code = 2; }
//	message QueryPathPB       { string path = 1; }          // "/Team/Sub/Name": a leading slash, no trailing one
//	message NqeQuerySourcePB  { string source_code = 1; }
//
// Written and read here by hand (four length-delimited fields need no protobuf library). This is NOT the fwdctl tree format: a zip of <name>.nqe files is refused by the
// Forward UI with "Invalid import; missing file queries-export.proto".
const uiPackageEntry = "queries-export.proto"

type uiQuery struct{ Path, Source string }

func pbVarint(b []byte, v uint64) []byte {
	for v >= 0x80 {
		b = append(b, byte(v)|0x80)
		v >>= 7
	}
	return append(b, byte(v))
}

// pbString appends field number n (wire type 2) holding s.
func pbString(b []byte, n int, s []byte) []byte {
	b = pbVarint(b, uint64(n)<<3|2)
	b = pbVarint(b, uint64(len(s)))
	return append(b, s...)
}

// encodeUIPackage returns the queries-export.proto bytes for the queries, in the order given.
func encodeUIPackage(qs []uiQuery) []byte {
	var top []byte
	for _, q := range qs {
		path := pbString(nil, 1, []byte(q.Path))
		src := pbString(nil, 1, []byte(q.Source))
		var one []byte
		one = pbString(one, 1, path)
		one = pbString(one, 2, src)
		top = pbString(top, 1, one)
	}
	return top
}

// zipUIPackage wraps the proto bytes as Forward does: one entry, queries-export.proto.
func zipUIPackage(qs []uiQuery) ([]byte, error) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.CreateHeader(&zip.FileHeader{Name: uiPackageEntry, Method: zip.Deflate})
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(encodeUIPackage(qs)); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pbFields reads a message's fields, keeping length-delimited ones; other wire types are skipped (a newer Forward may add fields).
func pbFields(b []byte) (map[int][][]byte, error) {
	out := map[int][][]byte{}
	for len(b) > 0 {
		key, n, err := pbReadVarint(b)
		if err != nil {
			return nil, err
		}
		b = b[n:]
		field, wire := int(key>>3), key&7
		switch wire {
		case 0:
			_, n, err := pbReadVarint(b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
		case 1:
			if len(b) < 8 {
				return nil, errors.New("truncated fixed64")
			}
			b = b[8:]
		case 5:
			if len(b) < 4 {
				return nil, errors.New("truncated fixed32")
			}
			b = b[4:]
		case 2:
			l, n, err := pbReadVarint(b)
			if err != nil {
				return nil, err
			}
			b = b[n:]
			if uint64(len(b)) < l {
				return nil, errors.New("truncated field")
			}
			out[field] = append(out[field], b[:l])
			b = b[l:]
		default:
			return nil, fmt.Errorf("unsupported wire type %d", wire)
		}
	}
	return out, nil
}

func pbReadVarint(b []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(b) && i < 10; i++ {
		v |= uint64(b[i]&0x7f) << (7 * uint(i))
		if b[i] < 0x80 {
			return v, i + 1, nil
		}
	}
	return 0, 0, errors.New("bad varint")
}

// decodeUIPackage reads the queries out of queries-export.proto bytes.
func decodeUIPackage(b []byte) ([]uiQuery, error) {
	top, err := pbFields(b)
	if err != nil {
		return nil, err
	}
	var out []uiQuery
	for _, raw := range top[1] {
		one, err := pbFields(raw)
		if err != nil {
			return nil, err
		}
		if len(one[1]) != 1 || len(one[2]) != 1 {
			return nil, errors.New("an exported query needs a path and a source")
		}
		p, err := pbFields(one[1][0])
		if err != nil {
			return nil, err
		}
		s, err := pbFields(one[2][0])
		if err != nil {
			return nil, err
		}
		if len(p[1]) != 1 {
			return nil, errors.New("an exported query has no path")
		}
		q := uiQuery{Path: string(p[1][0])}
		if len(s[1]) > 0 {
			q.Source = string(s[1][0])
		}
		out = append(out, q)
	}
	return out, nil
}

// readUIPackage reads a zip in Forward's export format. ok is false when the zip has no queries-export.proto (so the caller can say what it does hold).
func readUIPackage(zipBytes []byte) (qs []uiQuery, ok bool, err error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, false, err
	}
	for _, f := range zr.File {
		if f.Name != uiPackageEntry {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, true, err
		}
		b, err := io.ReadAll(io.LimitReader(rc, 64<<20))
		rc.Close()
		if err != nil {
			return nil, true, err
		}
		qs, err = decodeUIPackage(b)
		return qs, true, err
	}
	return nil, false, nil
}
