// Package otlp decodes OTLP/JSON log export batches and forwards them
// upstream (spec 071).
package otlp

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/sunstoneinstitute/worklode/internal/model"
)

// Record is one decoded OTLP log record, attributed from its resource and
// log-record attributes (spec 071 §1). Task is empty when the batch carries
// no worklode.task.id — the caller decides whether to store or only forward
// such a record.
type Record struct {
	Task    string
	Project string
	Session string
	Service string
	Event   string
	At      time.Time
	Attrs   model.ActivityAttrs
}

// exportRequest is the minimal ExportLogsServiceRequest shape this package
// reads; unrecognised fields (schemaUrl, scope, instrumentationScope, ...)
// are ignored by encoding/json.
type exportRequest struct {
	ResourceLogs []struct {
		Resource struct {
			Attributes []keyValue `json:"attributes"`
		} `json:"resource"`
		ScopeLogs []struct {
			LogRecords []struct {
				TimeUnixNano numOrString `json:"timeUnixNano"`
				Attributes   []keyValue  `json:"attributes"`
			} `json:"logRecords"`
		} `json:"scopeLogs"`
	} `json:"resourceLogs"`
}

type keyValue struct {
	Key   string   `json:"key"`
	Value anyValue `json:"value"`
}

// anyValue decodes the OTLP AnyValue shapes this package cares about:
// stringValue, intValue, boolValue, doubleValue. Other shapes (arrayValue,
// kvlistValue, bytesValue) decode to a zero anyValue and are dropped.
type anyValue struct {
	StringValue *string      `json:"stringValue"`
	IntValue    *numOrString `json:"intValue"`
	BoolValue   *bool        `json:"boolValue"`
	DoubleValue *float64     `json:"doubleValue"`
}

func (v anyValue) asString() (string, bool) {
	if v.StringValue != nil {
		return *v.StringValue, true
	}
	return "", false
}

func (v anyValue) asInt64() (int64, bool) {
	switch {
	case v.IntValue != nil:
		return int64(*v.IntValue), true
	case v.DoubleValue != nil && *v.DoubleValue == math.Trunc(*v.DoubleValue):
		return int64(*v.DoubleValue), true
	}
	return 0, false
}

func (v anyValue) asBool() (bool, bool) {
	if v.BoolValue != nil {
		return *v.BoolValue, true
	}
	return false, false
}

// numOrString decodes an OTLP integer field that the OTLP/JSON spec encodes
// as a string of digits but some exporters send as a bare JSON number. null
// and "" decode to zero: an exporter that omits a value must not fail the
// whole batch.
type numOrString int64

func (n *numOrString) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*n = numOrString(v)
	return nil
}

// DecodeLogs decodes an OTLP/JSON ExportLogsServiceRequest body into
// Records, one per log record across every resource and scope.
func DecodeLogs(body []byte) ([]Record, error) {
	var req exportRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, err
	}

	// A record with no timeUnixNano is stamped on arrival: an undated row
	// would sort as 1970 on the task page.
	now := time.Now()

	var records []Record
	for _, rl := range req.ResourceLogs {
		task := findString(rl.Resource.Attributes, "worklode.task.id")
		project := findString(rl.Resource.Attributes, "worklode.project.id")
		service := findString(rl.Resource.Attributes, "service.name")
		resSession := findString(rl.Resource.Attributes, "session.id")
		resEvent := findString(rl.Resource.Attributes, "event.name")

		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				session := findString(lr.Attributes, "session.id")
				if session == "" {
					session = resSession
				}
				event := findString(lr.Attributes, "event.name")
				if event == "" {
					event = resEvent
				}

				at := now
				if lr.TimeUnixNano != 0 {
					at = time.Unix(0, int64(lr.TimeUnixNano))
				}
				records = append(records, Record{
					Task:    task,
					Project: project,
					Session: session,
					Service: service,
					Event:   event,
					At:      at,
					Attrs:   decodeAttrs(lr.Attributes),
				})
			}
		}
	}
	return records, nil
}

// findString returns the string value of the first attribute keyed key, or
// "" if absent or not a stringValue.
func findString(kvs []keyValue, key string) string {
	for _, kv := range kvs {
		if kv.Key == key {
			s, _ := kv.Value.asString()
			return s
		}
	}
	return ""
}

// decodeAttrs fills a model.ActivityAttrs from a log record's attributes.
// This switch is the allowlist (spec 071 §2, 063 §3): a key it doesn't name
// — the body, prompt/response text, tool content, user.*, anything else —
// is dropped.
func decodeAttrs(kvs []keyValue) model.ActivityAttrs {
	var a model.ActivityAttrs
	for _, kv := range kvs {
		v := kv.Value
		switch kv.Key {
		case "tool_name":
			a.ToolName, _ = v.asString()
		case "success":
			if b, ok := v.asBool(); ok {
				a.Success = &b
			}
		case "duration_ms":
			a.DurationMS, _ = v.asInt64()
		case "error_type":
			a.ErrorType, _ = v.asString()
		case "decision_type":
			a.DecisionType, _ = v.asString()
		case "decision_source":
			a.DecisionSource, _ = v.asString()
		case "model":
			a.Model, _ = v.asString()
		case "request_id":
			a.RequestID, _ = v.asString()
		case "attempt":
			a.Attempt, _ = v.asInt64()
		case "status_code":
			a.StatusCode, _ = v.asInt64()
		case "error":
			a.Error, _ = v.asString()
		case "prompt_length":
			a.PromptLength, _ = v.asInt64()
		case "response_length":
			a.ResponseLength, _ = v.asInt64()
		case "command_name":
			a.CommandName, _ = v.asString()
		case "tool_use_id":
			a.ToolUseID, _ = v.asString()
		case "tool_input_size_bytes":
			a.ToolInputSizeBytes, _ = v.asInt64()
		case "tool_result_size_bytes":
			a.ToolResultSizeBytes, _ = v.asInt64()
		}
	}
	return a
}
