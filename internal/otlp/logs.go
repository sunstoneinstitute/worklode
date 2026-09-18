// Package otlp decodes OTLP/JSON log export batches and forwards them
// upstream (spec 071). It imports the standard library only.
package otlp

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"
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
	Attrs   map[string]any
}

// attrAllowlist is the only attrs keys a Record.Attrs may carry (spec 071
// §2, 063 §3). Everything else — the body, prompt/response text, tool
// content, user.* — is dropped.
var attrAllowlist = map[string]bool{
	"tool_name": true, "success": true, "duration_ms": true,
	"error_type": true, "decision_type": true, "decision_source": true,
	"model": true, "request_id": true, "attempt": true, "status_code": true,
	"error": true, "prompt_length": true, "response_length": true,
	"command_name": true, "tool_use_id": true, "tool_input_size_bytes": true,
	"tool_result_size_bytes": true,
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

func (v anyValue) toAny() any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.IntValue != nil:
		return int64(*v.IntValue)
	case v.BoolValue != nil:
		return *v.BoolValue
	case v.DoubleValue != nil:
		return *v.DoubleValue
	default:
		return nil
	}
}

// numOrString decodes an OTLP integer field that the OTLP/JSON spec encodes
// as a string of digits but some exporters send as a bare JSON number.
type numOrString int64

func (n *numOrString) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
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

	var records []Record
	for _, rl := range req.ResourceLogs {
		resAttrs := attrMap(rl.Resource.Attributes)
		task, _ := resAttrs["worklode.task.id"].(string)
		project, _ := resAttrs["worklode.project.id"].(string)
		service, _ := resAttrs["service.name"].(string)
		resSession, _ := resAttrs["session.id"].(string)
		resEvent, _ := resAttrs["event.name"].(string)

		for _, sl := range rl.ScopeLogs {
			for _, lr := range sl.LogRecords {
				recAttrs := attrMap(lr.Attributes)

				session, _ := recAttrs["session.id"].(string)
				if session == "" {
					session = resSession
				}
				event, _ := recAttrs["event.name"].(string)
				if event == "" {
					event = resEvent
				}

				records = append(records, Record{
					Task:    task,
					Project: project,
					Session: session,
					Service: service,
					Event:   event,
					At:      time.Unix(0, int64(lr.TimeUnixNano)),
					Attrs:   allowlisted(recAttrs),
				})
			}
		}
	}
	return records, nil
}

func attrMap(kvs []keyValue) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		if v := kv.Value.toAny(); v != nil {
			m[kv.Key] = v
		}
	}
	return m
}

func allowlisted(attrs map[string]any) map[string]any {
	out := make(map[string]any, len(attrs))
	for k, v := range attrs {
		if attrAllowlist[k] {
			out[k] = v
		}
	}
	return out
}
