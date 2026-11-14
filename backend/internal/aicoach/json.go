package aicoach

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

func nonNull(value json.RawMessage) bool { return len(value) > 0 && string(value) != "null" }

// ParseResult decodes one complete result and rejects unknown or missing fields.
// Pointer fields distinguish a required zero/empty value from a missing/null
// field. Strict output is checked again locally rather than trusted blindly.
func ParseResult(text string) (Result, error) {
	var wire struct {
		Title    *string `json:"title"`
		Summary  *string `json:"summary"`
		Insights *[]struct {
			Text    *string   `json:"text"`
			FactIDs *[]string `json:"factIds"`
		} `json:"insights"`
		NextAction *string         `json:"nextAction"`
		Draft      json.RawMessage `json:"draft"`
	}
	if err := decodeStrict(text, &wire); err != nil || wire.Title == nil || wire.Summary == nil || wire.Insights == nil || wire.NextAction == nil || len(wire.Draft) == 0 {
		return Result{}, ErrInvalidResult
	}
	result := Result{Title: *wire.Title, Summary: *wire.Summary, NextAction: *wire.NextAction, Insights: make([]Insight, 0, len(*wire.Insights))}
	for _, insight := range *wire.Insights {
		if insight.Text == nil || insight.FactIDs == nil {
			return Result{}, ErrInvalidResult
		}
		result.Insights = append(result.Insights, Insight{Text: *insight.Text, FactIDs: *insight.FactIDs})
	}
	if !nonNull(wire.Draft) {
		return result, nil
	}
	var draft struct {
		Date      *string         `json:"date"`
		Status    *string         `json:"status"`
		SessionID *string         `json:"sessionId"`
		Sleep     json.RawMessage `json:"sleep"`
		Fatigue   json.RawMessage `json:"fatigue"`
	}
	if err := decodeStrict(string(wire.Draft), &draft); err != nil || draft.Date == nil || draft.Status == nil || draft.SessionID == nil || len(draft.Sleep) == 0 || len(draft.Fatigue) == 0 {
		return Result{}, ErrInvalidResult
	}
	result.Draft = &Draft{Date: *draft.Date, Status: *draft.Status, SessionID: *draft.SessionID}
	if err := json.Unmarshal(draft.Sleep, &result.Draft.Sleep); err != nil {
		return Result{}, ErrInvalidResult
	}
	if err := json.Unmarshal(draft.Fatigue, &result.Draft.Fatigue); err != nil {
		return Result{}, ErrInvalidResult
	}
	return result, nil
}

func decodeStrict(text string, value any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return fmt.Errorf("expected one JSON object")
	}
	return nil
}
