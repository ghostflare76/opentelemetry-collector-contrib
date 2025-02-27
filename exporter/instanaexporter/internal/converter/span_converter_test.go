// Copyright 2022, OpenTelemetry Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package converter

import (
	"bytes"
	"encoding/json"
	"math/rand"
	"testing"
	"time"

	"go.opentelemetry.io/collector/pdata/pcommon"
	"go.opentelemetry.io/collector/pdata/ptrace"
	conventions "go.opentelemetry.io/collector/semconv/v1.8.0"

	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/instanaexporter/internal/backend"
	"github.com/open-telemetry/opentelemetry-collector-contrib/exporter/instanaexporter/internal/converter/model"
)

type SpanOptions struct {
	TraceID        [16]byte
	SpanID         [8]byte
	ParentID       [8]byte
	Error          string
	StartTimestamp time.Duration
	EndTimestamp   time.Duration
}

func setupSpan(span ptrace.Span, opts SpanOptions) {
	var empty16 [16]byte
	var empty8 [8]byte

	now := time.Now().UnixMilli()

	traceID := opts.TraceID
	spanID := opts.SpanID
	parentID := opts.ParentID
	startTime := opts.StartTimestamp
	endTime := opts.EndTimestamp

	if bytes.Equal(traceID[:], empty16[:]) {
		traceID = generateTraceID()
	}

	if bytes.Equal(spanID[:], empty8[:]) {
		spanID = generateSpanID()
	}

	if startTime == time.Second*0 {
		startTime = time.Duration(now)
	}

	if endTime == time.Second*0 {
		endTime = startTime + 1000
	}

	if opts.Error != "" {
		span.Status().SetCode(ptrace.StatusCodeError)
		span.Status().SetMessage(opts.Error)
	}

	if !bytes.Equal(parentID[:], empty8[:]) {
		span.SetParentSpanID(parentID)
	}

	span.SetStartTimestamp(pcommon.Timestamp(startTime * 1e6))
	span.SetEndTimestamp(pcommon.Timestamp(endTime * 1e6))

	span.SetSpanID(spanID)
	span.SetKind(ptrace.SpanKindClient)
	span.SetName("my_operation")
	span.TraceStateStruct().FromRaw("")
	span.SetTraceID(traceID)

	// adding attributes (tags in the instana side)
	span.Attributes().PutBool("some_key", true)
}

func generateAttrs() pcommon.Map {
	attrs := pcommon.NewMap()
	attrs.PutBool("some_boolean_key", true)
	attrs.PutString("custom_attribute", "ok")

	// test non empty pid
	attrs.PutString(conventions.AttributeProcessPID, "1234")

	// test non empty service name
	attrs.PutString(conventions.AttributeServiceName, "myservice")

	// test non empty instana host id
	attrs.PutString(backend.AttributeInstanaHostID, "myhost1")

	attrs.PutBool("itistrue", true)

	return attrs
}

func validateInstanaSpanBasics(sp model.Span, t *testing.T) {
	if sp.SpanID == "" {
		t.Error("expected span id not to be empty")
	}

	if sp.TraceID == "" {
		t.Error("expected trace id not to be empty")
	}

	if sp.Name != "otel" {
		t.Errorf("expected span name to be 'otel' but received '%v'", sp.Name)
	}

	if sp.Timestamp <= 0 {
		t.Errorf("expected timestamp to be provided but received %v", sp.Timestamp)
	}

	if sp.Duration <= 0 {
		t.Errorf("expected duration to be provided but received %v", sp.Duration)
	}
}

func validateBundle(jsonData []byte, t *testing.T, fn func(model.Span, *testing.T)) {
	var bundle model.Bundle

	err := json.Unmarshal(jsonData, &bundle)

	if err != nil {
		t.Fatal(err)
	}

	if len(bundle.Spans) == 0 {
		t.Log("bundle contains no spans")
		return
	}

	for _, span := range bundle.Spans {
		fn(span, t)
	}
}

func validateSpanError(sp model.Span, shouldHaveError bool, t *testing.T) {
	if shouldHaveError {
		if sp.Ec <= 0 {
			t.Error("expected span to have errors (ec = 1)")
		}

		if sp.Data.Tags[model.InstanaDataError] == "" {
			t.Error("expected data.error to exist")
		}

		if sp.Data.Tags[model.InstanaDataErrorDetail] == "" {
			t.Error("expected data.error_detail to exist")
		}

		return
	}

	if sp.Ec > 0 {
		t.Error("expected span not to have errors (ec = 0)")
	}

	if sp.Data.Tags[model.InstanaDataError] != "" {
		t.Error("expected data.error to be empty")
	}

	if sp.Data.Tags[model.InstanaDataErrorDetail] != "" {
		t.Error("expected data.error_detail to be empty")
	}
}

func TestSpanBasics(t *testing.T) {
	spanSlice := ptrace.NewSpanSlice()

	sp1 := spanSlice.AppendEmpty()

	setupSpan(sp1, SpanOptions{})

	attrs := generateAttrs()
	conv := SpanConverter{}
	bundle := conv.ConvertSpans(attrs, spanSlice)
	data, _ := json.MarshalIndent(bundle, "", "  ")

	validateBundle(data, t, func(sp model.Span, t *testing.T) {
		validateInstanaSpanBasics(sp, t)
		validateSpanError(sp, false, t)
	})
}

func TestSpanCorrelation(t *testing.T) {
	spanSlice := ptrace.NewSpanSlice()

	sp1 := spanSlice.AppendEmpty()
	setupSpan(sp1, SpanOptions{})

	sp2 := spanSlice.AppendEmpty()
	setupSpan(sp2, SpanOptions{
		ParentID: sp1.SpanID(),
	})

	sp3 := spanSlice.AppendEmpty()
	setupSpan(sp3, SpanOptions{
		ParentID: sp2.SpanID(),
	})

	sp4 := spanSlice.AppendEmpty()
	setupSpan(sp4, SpanOptions{
		ParentID: sp1.SpanID(),
	})

	attrs := generateAttrs()
	conv := SpanConverter{}
	bundle := conv.ConvertSpans(attrs, spanSlice)
	data, _ := json.MarshalIndent(bundle, "", "  ")

	spanIDList := make(map[string]bool)

	validateBundle(data, t, func(sp model.Span, t *testing.T) {
		validateInstanaSpanBasics(sp, t)
		validateSpanError(sp, false, t)

		spanIDList[sp.SpanID] = true

		if sp.ParentID != "" && !spanIDList[sp.ParentID] {
			t.Errorf("span %v expected to have parent id %v", sp.SpanID, sp.ParentID)
		}
	})
}
func TestSpanWithError(t *testing.T) {
	spanSlice := ptrace.NewSpanSlice()

	sp1 := spanSlice.AppendEmpty()
	setupSpan(sp1, SpanOptions{
		Error: "some error",
	})

	attrs := generateAttrs()
	conv := SpanConverter{}
	bundle := conv.ConvertSpans(attrs, spanSlice)
	data, _ := json.MarshalIndent(bundle, "", "  ")

	validateBundle(data, t, func(sp model.Span, t *testing.T) {
		validateInstanaSpanBasics(sp, t)
		validateSpanError(sp, true, t)
	})
}

func generateTraceID() (data [16]byte) {
	rand.Read(data[:])

	return data
}

func generateSpanID() (data [8]byte) {
	rand.Read(data[:])

	return data
}
