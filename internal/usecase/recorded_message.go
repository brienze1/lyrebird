package usecase

import "encoding/json"

// RecordedMessage is the plaintext shape sealed into domain.TrafficRecord's
// opaque Request/Response blobs — the one format shared by the write path
// (RecordTraffic) and every read path (GetTraffic/ListTraffic consumers).
// Deliberately uses map[string][]string rather than http.Header so this
// package stays free of any net/http dependency; http.Header IS
// map[string][]string, so converting at the adapter boundary is a type
// conversion, not a copy.
type RecordedMessage struct {
	Headers       map[string][]string `json:"headers"`
	Body          []byte              `json:"body"`
	BodyTruncated bool                `json:"body_truncated"`
	BodyTotalSize int64               `json:"body_total_size"`
}

// EncodeRecordedMessage serializes m for storage.
func EncodeRecordedMessage(m RecordedMessage) ([]byte, error) {
	return json.Marshal(m)
}

// DecodeRecordedMessage reverses EncodeRecordedMessage.
func DecodeRecordedMessage(raw []byte) (RecordedMessage, error) {
	var m RecordedMessage
	err := json.Unmarshal(raw, &m)
	return m, err
}
