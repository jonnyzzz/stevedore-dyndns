package mtproto

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrNotTLS is returned when the peeked bytes are not a TLS record carrying
// a ClientHello handshake message.
var ErrNotTLS = errors.New("mtproto: not a TLS ClientHello")

// PeekSNI reads a single TLS record from r, parses the enclosed ClientHello,
// and returns (sni, rawBytes, err) where:
//
//   - sni: the server_name from the SNI extension, "" if not present.
//   - rawBytes: the exact bytes consumed from r. Callers must replay these
//     bytes in front of r when forwarding the connection downstream.
//   - err: ErrNotTLS for anything that cannot be identified as a TLS
//     ClientHello; io.EOF / io.ErrUnexpectedEOF propagate as-is.
//
// The function performs a bounded read: exactly 5 bytes for the TLS record
// header, then exactly record_length bytes (capped at 16_640 ≈ 2^14 + 256 for
// future-proofing). It never issues unbounded Reads. Callers should set a
// read deadline on the underlying conn before calling.
func PeekSNI(r io.Reader) (string, []byte, error) {
	// TLS record header (RFC 8446 §5.1):
	//   content_type  (1 byte)  — 0x16 = handshake
	//   legacy_version (2 bytes) — 0x0301..0x0304
	//   length        (2 bytes) — record payload length, big-endian
	header := make([]byte, 5)
	if _, err := io.ReadFull(r, header); err != nil {
		return "", nil, err
	}
	if header[0] != 0x16 {
		return "", header, fmt.Errorf("%w: content_type=%#x", ErrNotTLS, header[0])
	}
	// Accept any TLS version in the legacy_version field — real clients use
	// 0x0301 ("TLS 1.0") for backwards compatibility. We only reject obvious
	// garbage.
	if header[1] != 0x03 {
		return "", header, fmt.Errorf("%w: legacy_version=%#x%02x", ErrNotTLS, header[1], header[2])
	}
	recordLen := int(binary.BigEndian.Uint16(header[3:5]))
	const maxRecord = 16 * 1024 // 16 KiB: standard TLS record max.
	if recordLen == 0 || recordLen > maxRecord {
		return "", header, fmt.Errorf("%w: record length %d out of range", ErrNotTLS, recordLen)
	}

	body := make([]byte, recordLen)
	if _, err := io.ReadFull(r, body); err != nil {
		return "", append(header, body...), err
	}
	raw := append(header, body...)

	host, err := parseClientHelloSNI(body)
	if err != nil {
		return "", raw, err
	}
	return host, raw, nil
}

// parseClientHelloSNI parses the handshake record payload (immediately after
// the 5-byte TLS record header) and returns the SNI. Only the first
// ClientHello in the record is inspected; additional messages are ignored.
func parseClientHelloSNI(b []byte) (string, error) {
	// Handshake header (RFC 8446 §4):
	//   msg_type (1 byte)  — 0x01 = ClientHello
	//   length   (3 bytes) — big-endian body length
	if len(b) < 4 {
		return "", fmt.Errorf("%w: handshake header truncated", ErrNotTLS)
	}
	if b[0] != 0x01 {
		return "", fmt.Errorf("%w: handshake msg_type=%#x", ErrNotTLS, b[0])
	}
	bodyLen := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
	if bodyLen+4 > len(b) {
		return "", fmt.Errorf("%w: handshake body length %d > record payload %d", ErrNotTLS, bodyLen, len(b)-4)
	}
	body := b[4 : 4+bodyLen]

	// ClientHello body:
	//   legacy_version  (2 bytes)
	//   random         (32 bytes)
	//   session_id     (opaque<0..32>)
	//   cipher_suites  (opaque<2..2^16-2>)
	//   compression_methods (opaque<1..2^8-1>)
	//   extensions     (opaque<8..2^16-1>)   ← present in TLS 1.0+ with extensions
	const minHeader = 2 + 32
	if len(body) < minHeader+1 {
		return "", fmt.Errorf("%w: ClientHello body too short", ErrNotTLS)
	}
	pos := minHeader

	// session_id
	if len(body) < pos+1 {
		return "", fmt.Errorf("%w: truncated before session_id", ErrNotTLS)
	}
	sidLen := int(body[pos])
	pos++
	if len(body) < pos+sidLen {
		return "", fmt.Errorf("%w: truncated session_id", ErrNotTLS)
	}
	pos += sidLen

	// cipher_suites
	if len(body) < pos+2 {
		return "", fmt.Errorf("%w: truncated before cipher_suites length", ErrNotTLS)
	}
	csLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body) < pos+csLen {
		return "", fmt.Errorf("%w: truncated cipher_suites", ErrNotTLS)
	}
	pos += csLen

	// compression_methods
	if len(body) < pos+1 {
		return "", fmt.Errorf("%w: truncated before compression_methods length", ErrNotTLS)
	}
	cmLen := int(body[pos])
	pos++
	if len(body) < pos+cmLen {
		return "", fmt.Errorf("%w: truncated compression_methods", ErrNotTLS)
	}
	pos += cmLen

	// extensions (optional — TLS 1.0 may omit).
	if len(body) < pos+2 {
		return "", nil // no extensions, no SNI.
	}
	extLen := int(binary.BigEndian.Uint16(body[pos : pos+2]))
	pos += 2
	if len(body) < pos+extLen {
		return "", fmt.Errorf("%w: truncated extensions", ErrNotTLS)
	}
	extEnd := pos + extLen
	for pos+4 <= extEnd {
		extType := binary.BigEndian.Uint16(body[pos : pos+2])
		extDataLen := int(binary.BigEndian.Uint16(body[pos+2 : pos+4]))
		pos += 4
		if pos+extDataLen > extEnd {
			return "", fmt.Errorf("%w: truncated extension body", ErrNotTLS)
		}
		if extType == 0x0000 { // server_name extension
			host, err := parseSNIExtension(body[pos : pos+extDataLen])
			if err != nil {
				return "", err
			}
			return host, nil
		}
		pos += extDataLen
	}
	return "", nil
}

// parseSNIExtension extracts the first host_name (type 0) from an
// rfc6066 server_name_list.
func parseSNIExtension(b []byte) (string, error) {
	if len(b) < 2 {
		return "", fmt.Errorf("%w: SNI list length missing", ErrNotTLS)
	}
	listLen := int(binary.BigEndian.Uint16(b[:2]))
	if listLen+2 > len(b) {
		return "", fmt.Errorf("%w: SNI list truncated", ErrNotTLS)
	}
	pos := 2
	end := 2 + listLen
	for pos+3 <= end {
		nameType := b[pos]
		nameLen := int(binary.BigEndian.Uint16(b[pos+1 : pos+3]))
		pos += 3
		if pos+nameLen > end {
			return "", fmt.Errorf("%w: SNI name truncated", ErrNotTLS)
		}
		if nameType == 0 { // host_name
			return string(b[pos : pos+nameLen]), nil
		}
		pos += nameLen
	}
	return "", nil
}
