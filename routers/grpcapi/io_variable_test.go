package grpcapi

import (
	"context"
	"path/filepath"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	pb "nano_ctl/pkg/proto/godmc/v1"
	"nano_ctl/service/store"
)

func TestVariableRPCValidationAndPersistence(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "config.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server := New(nil, db)
	ctx := context.Background()
	ref := &pb.IOVariableRef{CardNo: 0, Kind: pb.IOKind_IO_KIND_OUTPUT_BIT, Address: 65535}
	saved, err := server.PutIOVariable(ctx, &pb.IOVariable{Ref: ref, Alias: "RPC 输出", Disabled: true, Reverse: true})
	if err != nil || saved.GetAlias() != "RPC 输出" {
		t.Fatalf("save: %v %v", saved, err)
	}
	list, err := server.ListIOVariables(ctx, &pb.IOVariablesRequest{})
	if err != nil || len(list.GetVariables()) != 1 {
		t.Fatalf("list: %v %v", list, err)
	}
	if _, err := server.ReadIOVariable(ctx, ref); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled: %v", err)
	}
	if _, err := server.WriteIOVariable(ctx, &pb.IOVariableWriteRequest{Ref: ref, On: true}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("disabled write: %v", err)
	}
	if _, err := server.DeleteIOVariable(ctx, ref); err != nil {
		t.Fatal(err)
	}
	if _, err := server.ReadIOVariable(ctx, ref); status.Code(err) != codes.NotFound {
		t.Fatalf("missing: %v", err)
	}
	if _, err := server.PutIOVariable(ctx, nil); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("nil ref: %v", err)
	}
	ref.CardNo = 65536
	if _, err := server.PutIOVariable(ctx, &pb.IOVariable{Ref: ref, Alias: "overflow"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("overflow: %v", err)
	}
	ref.CardNo = 0
	ref.Kind = pb.IOKind(999)
	if _, err := server.PutIOVariable(ctx, &pb.IOVariable{Ref: ref, Alias: "bad kind"}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("kind: %v", err)
	}
}
