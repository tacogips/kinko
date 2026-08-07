package kinko

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

type syncProgressMode string

const (
	syncProgressAuto  syncProgressMode = "auto"
	syncProgressPlain syncProgressMode = "plain"
	syncProgressNone  syncProgressMode = "none"
	syncProgressJSONL syncProgressMode = "jsonl"
)

type syncProgressEvent struct {
	Operation string `json:"operation"`
	Phase     string `json:"phase"`
	ActionID  string `json:"action_id,omitempty"`
	EntryID   string `json:"entry_id,omitempty"`
	Status    string `json:"status"`
}

type syncProgressSink interface {
	Emit(syncProgressEvent) error
}

type syncWriterProgress struct {
	mode   syncProgressMode
	writer io.Writer
}

func (sink syncWriterProgress) Emit(event syncProgressEvent) error {
	if sink.mode == syncProgressNone {
		return nil
	}
	if sink.writer == nil {
		return errors.New("sync progress writer is nil")
	}
	if event.Operation == "" || event.Phase == "" || event.Status == "" {
		return errors.New("sync progress event is incomplete")
	}
	if sink.mode == syncProgressJSONL {
		return json.NewEncoder(sink.writer).Encode(event)
	}
	_, err := fmt.Fprintf(sink.writer, "%s %s action=%s entry=%s status=%s\n", event.Operation, event.Phase, event.ActionID, event.EntryID, event.Status)
	return err
}

type discardSyncProgress struct{}

func (discardSyncProgress) Emit(syncProgressEvent) error { return nil }
