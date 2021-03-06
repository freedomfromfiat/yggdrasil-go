package yggdrasil

import (
	"errors"
	"fmt"
	"io"

	"github.com/yggdrasil-network/yggdrasil-go/src/util"
)

// Test that this matches the interface we expect
var _ = linkInterfaceMsgIO(&stream{})

type stream struct {
	rwc          io.ReadWriteCloser
	inputBuffer  []byte                  // Incoming packet stream
	frag         [2 * streamMsgSize]byte // Temporary data read off the underlying rwc, on its way to the inputBuffer
	outputBuffer [2 * streamMsgSize]byte // Temporary data about to be written to the rwc
}

func (s *stream) close() error {
	return s.rwc.Close()
}

const streamMsgSize = 2048 + 65535

var streamMsg = [...]byte{0xde, 0xad, 0xb1, 0x75} // "dead bits"

func (s *stream) init(rwc io.ReadWriteCloser) {
	// TODO have this also do the metadata handshake and create the peer struct
	s.rwc = rwc
	// TODO call something to do the metadata exchange
}

// writeMsg writes a message with stream padding, and is *not* thread safe.
func (s *stream) writeMsg(bs []byte) (int, error) {
	buf := s.outputBuffer[:0]
	buf = append(buf, streamMsg[:]...)
	buf = wire_put_uint64(uint64(len(bs)), buf)
	padLen := len(buf)
	buf = append(buf, bs...)
	var bn int
	for bn < len(buf) {
		n, err := s.rwc.Write(buf[bn:])
		bn += n
		if err != nil {
			l := bn - padLen
			if l < 0 {
				l = 0
			}
			return l, err
		}
	}
	return len(bs), nil
}

// readMsg reads a message from the stream, accounting for stream padding, and is *not* thread safe.
func (s *stream) readMsg() ([]byte, error) {
	for {
		buf := s.inputBuffer
		msg, ok, err := stream_chopMsg(&buf)
		switch {
		case err != nil:
			// Something in the stream format is corrupt
			return nil, fmt.Errorf("message error: %v", err)
		case ok:
			// Copy the packet into bs, shift the buffer, and return
			msg = append(util.GetBytes(), msg...)
			s.inputBuffer = append(s.inputBuffer[:0], buf...)
			return msg, nil
		default:
			// Wait for the underlying reader to return enough info for us to proceed
			n, err := s.rwc.Read(s.frag[:])
			if n > 0 {
				s.inputBuffer = append(s.inputBuffer, s.frag[:n]...)
			} else if err != nil {
				return nil, err
			}
		}
	}
}

// Writes metadata bytes without stream padding, meant to be temporary
func (s *stream) _sendMetaBytes(metaBytes []byte) error {
	var written int
	for written < len(metaBytes) {
		n, err := s.rwc.Write(metaBytes)
		written += n
		if err != nil {
			return err
		}
	}
	return nil
}

// Reads metadata bytes without stream padding, meant to be temporary
func (s *stream) _recvMetaBytes() ([]byte, error) {
	var meta version_metadata
	frag := meta.encode()
	metaBytes := make([]byte, 0, len(frag))
	for len(metaBytes) < len(frag) {
		n, err := s.rwc.Read(frag)
		if err != nil {
			return nil, err
		}
		metaBytes = append(metaBytes, frag[:n]...)
	}
	return metaBytes, nil
}

// This takes a pointer to a slice as an argument. It checks if there's a
// complete message and, if so, slices out those parts and returns the message,
// true, and nil. If there's no error, but also no complete message, it returns
// nil, false, and nil. If there's an error, it returns nil, false, and the
// error, which the reader then handles (currently, by returning from the
// reader, which causes the connection to close).
func stream_chopMsg(bs *[]byte) ([]byte, bool, error) {
	// Returns msg, ok, err
	if len(*bs) < len(streamMsg) {
		return nil, false, nil
	}
	for idx := range streamMsg {
		if (*bs)[idx] != streamMsg[idx] {
			return nil, false, errors.New("bad message")
		}
	}
	msgLen, msgLenLen := wire_decode_uint64((*bs)[len(streamMsg):])
	if msgLen > streamMsgSize {
		return nil, false, errors.New("oversized message")
	}
	msgBegin := len(streamMsg) + msgLenLen
	msgEnd := msgBegin + int(msgLen)
	if msgLenLen == 0 || len(*bs) < msgEnd {
		// We don't have the full message
		// Need to buffer this and wait for the rest to come in
		return nil, false, nil
	}
	msg := (*bs)[msgBegin:msgEnd]
	(*bs) = (*bs)[msgEnd:]
	return msg, true, nil
}
