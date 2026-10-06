// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 Daniel Rigoberto Jacobo Sandoval

package fake

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/danielrigobertojs/ai-usage-exporter/internal/model"
	"github.com/danielrigobertojs/ai-usage-exporter/internal/provider"
)

func TestFakeDescriptorIsValid(t *testing.T) {
	d := New(1).Descriptor()
	if err := d.Validate(); err != nil {
		t.Fatalf("Descriptor().Validate(): %v", err)
	}
	if d.ID != "fake" {
		t.Errorf("Descriptor().ID = %q, want %q", d.ID, "fake")
	}
}

func TestFakeParseIsDeterministic(t *testing.T) {
	src := provider.Source{Path: "/home/user/.fake/a.jsonl", ModTime: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}

	run := func() []model.UsageEvent {
		var got []model.UsageEvent
		p := New(3)
		err := p.Parse(context.Background(), src, func(e model.UsageEvent) error {
			got = append(got, e)
			return nil
		})
		if err != nil {
			t.Fatalf("Parse: unexpected error: %v", err)
		}
		return got
	}

	first, second := run(), run()
	if len(first) != 3 {
		t.Fatalf("len(events) = %d, want 3", len(first))
	}
	for i := range first {
		if !reflect.DeepEqual(first[i], second[i]) {
			t.Errorf("event %d not deterministic: %+v != %+v", i, first[i], second[i])
		}
		if err := first[i].Valid(); err != nil {
			t.Errorf("event %d invalid: %v", i, err)
		}
	}
}

func TestFakeParsePropagatesEmitError(t *testing.T) {
	wantErr := errors.New("boom")
	p := New(5)
	calls := 0
	err := p.Parse(context.Background(), provider.Source{}, func(model.UsageEvent) error {
		calls++
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("Parse: error = %v, want %v", err, wantErr)
	}
	if calls != 1 {
		t.Errorf("emit called %d times, want 1 (Parse must stop on first error)", calls)
	}
}

func TestFakeParseRespectsCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	p := New(5)
	calls := 0
	err := p.Parse(ctx, provider.Source{}, func(model.UsageEvent) error {
		calls++
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Parse: error = %v, want context.Canceled", err)
	}
	if calls != 0 {
		t.Errorf("emit called %d times, want 0 (Parse must stop before emitting)", calls)
	}
}
