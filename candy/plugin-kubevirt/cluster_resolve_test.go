package kubevirt

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opencharly/plugin-kubevirt/candy/plugin-kubevirt/params"
	"github.com/opencharly/sdk"
	pb "github.com/opencharly/spec/proto"
	"google.golang.org/grpc"
)

// clusterResolve_test.go pins the loud-resolution contract for opencharly/plugin-kubevirt#8:
// a `kubevirt:` step that NAMES a cluster (`in.Cluster != ""`) whose resolution FAILS must
// surface an error — never silently return, leaving `in.KubeContext == ""` and letting the
// caller fall back to the kubeconfig current-context (which produced the bare
// `no kubeconfig context selected` on every probe).

// fakeExecClient is a minimal pb.ExecutorServiceClient: every method panics EXCEPT
// HostBuild, which dispatches by req.Kind and returns connectErr for the
// "deploy-plugins-connect" leg — the project-dir-resolve failure the loud contract
// must surface.
type fakeExecClient struct {
	connectErr error
}

func (f *fakeExecClient) HostBuild(context.Context, *pb.HostBuildRequest, ...grpc.CallOption) (*pb.HostBuildReply, error) {
	if f.connectErr != nil {
		return nil, f.connectErr
	}
	return &pb.HostBuildReply{}, nil
}

func (f *fakeExecClient) Venue(context.Context, *pb.Empty, ...grpc.CallOption) (*pb.VenueReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunSystem(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunUser(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.RunReply, error) {
	panic("unused")
}
func (f *fakeExecClient) PutFile(context.Context, *pb.PutFileRequest, ...grpc.CallOption) (*pb.PutFileReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunCapture(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.CaptureReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunInteractive(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunStream(context.Context, *pb.RunRequest, ...grpc.CallOption) (*pb.LiveReply, error) {
	panic("unused")
}
func (f *fakeExecClient) GetFile(context.Context, *pb.GetFileRequest, ...grpc.CallOption) (*pb.GetFileReply, error) {
	panic("unused")
}
func (f *fakeExecClient) RunHostStep(context.Context, *pb.HostStepRequest, ...grpc.CallOption) (*pb.HostStepReply, error) {
	panic("unused")
}
func (f *fakeExecClient) InvokeProvider(context.Context, *pb.InvokeProviderRequest, ...grpc.CallOption) (*pb.InvokeReply, error) {
	panic("unused")
}
func (f *fakeExecClient) DescribeProvider(context.Context, *pb.DescribeProviderRequest, ...grpc.CallOption) (*pb.DescribeProviderReply, error) {
	panic("unused")
}

// TestResolveClusterContext_ProjectDirResolveFails_ErrorsLoudly pins the #8 fix: the
// deploy-plugins-connect leg fails → an error naming the cluster, not a silent return.
func TestResolveClusterContext_ProjectDirResolveFails_ErrorsLoudly(t *testing.T) {
	exec := sdk.NewInProcExecutor(&fakeExecClient{connectErr: errors.New("host seam down")})
	in := &params.KubeVirtInput{Cluster: "check-kubevirt-operator-ctx"}

	err := resolveClusterContext(context.Background(), exec, in)
	if err == nil {
		t.Fatal("resolveClusterContext: want an error when the project-dir resolve fails, got nil (the silent-swallow class)")
	}
	if !strings.Contains(err.Error(), "check-kubevirt-operator-ctx") {
		t.Fatalf("error must name the cluster, got: %v", err)
	}
	if in.KubeContext != "" {
		t.Fatalf("KubeContext must stay empty on failure, got %q", in.KubeContext)
	}
}

// TestResolveClusterContext_NilExecutor_ErrorsLoudly pins the no-executor path: it must
// NOT silently succeed into an empty context.
func TestResolveClusterContext_NilExecutor_ErrorsLoudly(t *testing.T) {
	in := &params.KubeVirtInput{Cluster: "check-kubevirt-operator-ctx"}

	err := resolveClusterContext(context.Background(), nil, in)
	if err == nil {
		t.Fatal("resolveClusterContext: want an error for a nil executor, got nil")
	}
	if !strings.Contains(err.Error(), "check-kubevirt-operator-ctx") {
		t.Fatalf("error must name the cluster, got: %v", err)
	}
}
