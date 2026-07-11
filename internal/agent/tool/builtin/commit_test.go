package builtin

import (
	"context"
	"testing"
)

func TestCommitToolRecordsRequest(t *testing.T) {
	ctx, holder := NewCommitRequestHolder(context.Background())
	got, err := NewCommitTool().InvokableRun(ctx, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if got == "" || holder.Get() == nil {
		t.Fatalf("InvokableRun() = %q, request = %#v", got, holder.Get())
	}
}
