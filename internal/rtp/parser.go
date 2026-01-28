package rtp

import (
	"fmt"

	"github.com/pion/rtp"
)

// ParsedPacket contains parsed RTP header information along with raw data
type ParsedPacket struct {
	Header    *rtp.Header
	Payload   []byte
	RawData   []byte
	Timestamp int64 // Server-side capture timestamp
}

// Parser handles RTP packet parsing
type Parser struct{}

// NewParser creates a new RTP parser
func NewParser() *Parser {
	return &Parser{}
}

// Parse parses raw RTP data and extracts header information
func (p *Parser) Parse(data []byte, serverTimestamp int64) (*ParsedPacket, error) {
	if len(data) < 12 {
		return nil, fmt.Errorf("rtp packet too short: %d bytes", len(data))
	}

	packet := &rtp.Packet{}
	if err := packet.Unmarshal(data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal rtp packet: %w", err)
	}

	return &ParsedPacket{
		Header:    &packet.Header,
		Payload:   packet.Payload,
		RawData:   data,
		Timestamp: serverTimestamp,
	}, nil
}

// GetSSRC extracts SSRC from raw RTP data without full parsing
func GetSSRC(data []byte) (uint32, error) {
	if len(data) < 12 {
		return 0, fmt.Errorf("rtp packet too short for SSRC extraction")
	}
	// SSRC is at bytes 8-11 (big-endian)
	ssrc := uint32(data[8])<<24 | uint32(data[9])<<16 | uint32(data[10])<<8 | uint32(data[11])
	return ssrc, nil
}

// GetPayloadType extracts payload type from raw RTP data
func GetPayloadType(data []byte) (uint8, error) {
	if len(data) < 2 {
		return 0, fmt.Errorf("rtp packet too short for payload type extraction")
	}
	// Payload type is in byte 1, bits 0-6
	return data[1] & 0x7F, nil
}

// GetSequenceNumber extracts sequence number from raw RTP data
func GetSequenceNumber(data []byte) (uint16, error) {
	if len(data) < 4 {
		return 0, fmt.Errorf("rtp packet too short for sequence number extraction")
	}
	// Sequence number is at bytes 2-3 (big-endian)
	return uint16(data[2])<<8 | uint16(data[3]), nil
}

// GetRTPTimestamp extracts RTP timestamp from raw RTP data
func GetRTPTimestamp(data []byte) (uint32, error) {
	if len(data) < 8 {
		return 0, fmt.Errorf("rtp packet too short for timestamp extraction")
	}
	// Timestamp is at bytes 4-7 (big-endian)
	return uint32(data[4])<<24 | uint32(data[5])<<16 | uint32(data[6])<<8 | uint32(data[7]), nil
}
