package mcpclient

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
)

func encodeRequest(id int64, method string, params json.RawMessage) (json.RawMessage, error) {
	var buf bytes.Buffer
	buf.WriteString(`{"jsonrpc":"2.0","id":`)
	buf.WriteString(fmt.Sprintf("%d", id))
	buf.WriteString(`,"method":`)
	mb, err := json.Marshal(method)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrProtocol, err)
	}
	buf.Write(mb)
	if params != nil {
		buf.WriteString(`,"params":`)
		buf.Write(params)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// decodeResponseResult extracts the result field from a JSON-RPC response.
// The result is returned as RawMessage (not re-encoded).
func decodeResponseResult(resp json.RawMessage, wantID int64) (json.RawMessage, error) {
	if len(resp) > MaxResponseBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(resp))
	}
	if !json.Valid(resp) {
		return nil, fmt.Errorf("%w: response", ErrMalformed)
	}

	var envelope struct {
		JSONRPC string          `json:"jsonrpc"`
		ID      *int64          `json:"id"`
		Result  json.RawMessage `json:"result"`
		Error   *struct {
			Code    int             `json:"code"`
			Message string          `json:"message"`
			Data    json.RawMessage `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(resp, &envelope); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("%w: json-rpc error %d: %s", ErrProtocol, envelope.Error.Code, envelope.Error.Message)
	}
	if envelope.ID == nil || *envelope.ID != wantID {
		return nil, fmt.Errorf("%w: response id mismatch", ErrProtocol)
	}
	if envelope.Result == nil {
		return nil, fmt.Errorf("%w: missing result", ErrProtocol)
	}
	return envelope.Result, nil
}

// extractProtocolVersion reads protocolVersion from an initialize result
// without discarding other fields (we only need this one scalar).
func extractProtocolVersion(result json.RawMessage) (string, error) {
	var v struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if err := json.Unmarshal(result, &v); err != nil {
		return "", fmt.Errorf("%w: initialize result: %v", ErrMalformed, err)
	}
	if v.ProtocolVersion == "" {
		return "", fmt.Errorf("%w: missing protocolVersion", ErrProtocol)
	}
	return v.ProtocolVersion, nil
}

// extractToolsRaw pulls each element of result.tools as json.RawMessage,
// preserving exact bytes for every tool object (including unknown fields).
func extractToolsRaw(result json.RawMessage) ([]json.RawMessage, error) {
	var envelope struct {
		Tools []json.RawMessage `json:"tools"`
	}
	dec := json.NewDecoder(bytes.NewReader(result))
	if err := dec.Decode(&envelope); err != nil {
		return nil, fmt.Errorf("%w: tools/list result: %v", ErrMalformed, err)
	}
	if envelope.Tools == nil {
		// Explicit empty list is fine; missing key → empty.
		return []json.RawMessage{}, nil
	}
	return envelope.Tools, nil
}

// limitedReader returns ErrTooLarge when more than max bytes are read.
type limitedReader struct {
	r     io.Reader
	remain int64
}

func newLimitedReader(r io.Reader, max int64) *limitedReader {
	return &limitedReader{r: r, remain: max}
}

func (l *limitedReader) Read(p []byte) (int, error) {
	if l.remain <= 0 {
		// Peek one more byte to distinguish exact-max from overflow.
		var one [1]byte
		n, err := l.r.Read(one[:])
		if n > 0 {
			return 0, fmt.Errorf("%w", ErrTooLarge)
		}
		return 0, err
	}
	if int64(len(p)) > l.remain {
		p = p[:l.remain]
	}
	n, err := l.r.Read(p)
	l.remain -= int64(n)
	if err == io.EOF {
		return n, err
	}
	if err != nil {
		return n, err
	}
	if l.remain == 0 {
		var one [1]byte
		n2, err2 := l.r.Read(one[:])
		if n2 > 0 {
			return n, fmt.Errorf("%w", ErrTooLarge)
		}
		if err2 != nil && err2 != io.EOF {
			return n, err2
		}
	}
	return n, err
}

func readAllLimited(r io.Reader, max int64) ([]byte, error) {
	lr := newLimitedReader(r, max)
	b, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if len(b) > int(max) {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(b))
	}
	return b, nil
}
