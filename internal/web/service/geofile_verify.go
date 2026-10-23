package service

import (
	"bufio"
	"errors"
	"fmt"
	"io"
)

// maxGeofileBytes caps a download the same way maxXrayArchiveBytes caps the
// binary. The largest file the allowlist points at is ~20 MB.
const maxGeofileBytes = 200 << 20

// errGeofileTruncated is what a partial download looks like from the inside.
var errGeofileTruncated = errors.New("the file ends in the middle of a record")

// A truncated protobuf list can still satisfy Xray's queried country codes.
// Verify whole-record framing in addition to the downloader's published digest.
func verifyGeofile(r io.Reader) error {
	br := bufio.NewReaderSize(r, 64*1024)
	records := 0
	for {
		// The record tag. EOF here — and only here — is a clean end of file.
		if _, err := br.ReadByte(); err != nil {
			if errors.Is(err, io.EOF) {
				if records == 0 {
					return errors.New("the file holds no records at all")
				}
				return nil
			}
			return err
		}
		length, err := readVarint(br)
		if err != nil {
			return fmt.Errorf("%w: reading the length of record %d: %w", errGeofileTruncated, records+1, err)
		}
		if length == 0 || length > maxGeofileBytes {
			return fmt.Errorf("record %d declares an implausible length of %d bytes", records+1, length)
		}
		if _, err := br.Discard(int(length)); err != nil {
			return fmt.Errorf("%w: record %d declares %d bytes and the file does not carry them: %w",
				errGeofileTruncated, records+1, length, err)
		}
		records++
	}
}

// readVarint is protobuf's base-128 encoding, LSB first, continuation bit 0x80.
func readVarint(br *bufio.Reader) (uint64, error) {
	var value uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := br.ReadByte()
		if err != nil {
			return 0, err
		}
		value |= (uint64(b) & 0x7F) << shift
		if b&0x80 == 0 {
			return value, nil
		}
	}
	return 0, errors.New("varint overflow")
}
