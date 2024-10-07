// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

// Unit tests for the pa package.
package pa

import (
	"context"
	"net"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/testing/protocmp"

	pbp "github.com/lowRISC/opentitan-provisioning/src/pa/proto/pa_go_pb"
	"github.com/lowRISC/opentitan-provisioning/src/pa/services/pa"
	pbr "github.com/lowRISC/opentitan-provisioning/src/registry_buffer/proto/registry_buffer_go_pb"
	pbs "github.com/lowRISC/opentitan-provisioning/src/spm/proto/spm_go_pb"
)

const (
	// bufferConnectionSize is the size of the gRPC connection buffer.
	bufferConnectionSize = 2048 * 1024
)

// bufferDialer creates a gRPC buffer connection to an initialized PA service.
// It returns a connection which can then be used to initialize the client
// interface by calling `pbp.NewProvisioningApplianceClient`.
func bufferDialer(t *testing.T, spmClient pbs.SpmServiceClient, rbClient pbr.RegistryBufferServiceClient) func(context.Context, string) (net.Conn, error) {
	listener := bufconn.Listen(bufferConnectionSize)
	server := grpc.NewServer()
	pbp.RegisterProvisioningApplianceServiceServer(server, pa.NewProvisioningApplianceServer(spmClient, rbClient, false))
	go func(t *testing.T) {
		if err := server.Serve(listener); err != nil {
			t.Fatal(err)
		}
	}(t)
	return func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}
}

type fakeRbClient struct {
	registerDevice registerDeviceResponse
}

type registerDeviceResponse struct {
	response *pbp.RegistrationResponse
	err      error
}

func (c *fakeRbClient) RegisterDevice(ctx context.Context, request *pbp.RegistrationRequest, opts ...grpc.CallOption) (*pbp.RegistrationResponse, error) {
	return c.registerDevice.response, c.registerDevice.err
}

// fakeSpmClient provides a fake client interface to the SPM server. Test
// cases can set the fake responses as part of the test setup.
type fakeSpmClient struct {
	initSession        initSessionResponse
	createKeyAndCert   createKeyAndCertFakeResponse
	deriveSymmetricKey deriveSymmetricKeyResponse
}

type initSessionResponse struct {
	response *pbp.InitSessionResponse
	err      error
}

type createKeyAndCertFakeResponse struct {
	response *pbp.CreateKeyAndCertResponse
	err      error
}

type deriveSymmetricKeyResponse struct {
	response *pbp.DeriveSymmetricKeyResponse
	err      error
}

func (c *fakeSpmClient) InitSession(ctx context.Context, request *pbp.InitSessionRequest, opts ...grpc.CallOption) (*pbp.InitSessionResponse, error) {
	return c.initSession.response, c.initSession.err
}

func (c *fakeSpmClient) CreateKeyAndCert(ctx context.Context, request *pbp.CreateKeyAndCertRequest, opts ...grpc.CallOption) (*pbp.CreateKeyAndCertResponse, error) {
	return c.createKeyAndCert.response, c.createKeyAndCert.err
}

func (c *fakeSpmClient) DeriveSymmetricKey(ctx context.Context, request *pbp.DeriveSymmetricKeyRequest, opts ...grpc.CallOption) (*pbp.DeriveSymmetricKeyResponse, error) {
	return c.deriveSymmetricKey.response, c.deriveSymmetricKey.err
}

func TestCreateKeyAndCert(t *testing.T) {
	ctx := context.Background()
	spmClient := &fakeSpmClient{}
	rbClient := &fakeRbClient{}
	conn, err := grpc.DialContext(ctx, "", grpc.WithInsecure(), grpc.WithContextDialer(bufferDialer(t, spmClient, rbClient)))
	if err != nil {
		t.Fatalf("failed to connect to test server: %v", err)
	}
	defer conn.Close()

	client := pbp.NewProvisioningApplianceServiceClient(conn)

	tests := []struct {
		name        string
		request     *pbp.CreateKeyAndCertRequest
		expCode     codes.Code
		spmResponse *pbp.CreateKeyAndCertResponse
		spmError    error
	}{
		{
			// This is a simple connectivity test. The request and expected
			// response values should be updated if there is additional
			// logic added to the PA service.
			name:        "ok",
			expCode:     codes.OK,
			request:     &pbp.CreateKeyAndCertRequest{},
			spmResponse: &pbp.CreateKeyAndCertResponse{},
			spmError:    nil,
		},
		{
			// SPM errors are converted to code.Internal.
			name:        "spm_error",
			expCode:     codes.Internal,
			request:     &pbp.CreateKeyAndCertRequest{},
			spmResponse: &pbp.CreateKeyAndCertResponse{},
			spmError:    status.Errorf(codes.InvalidArgument, "invalid argument"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spmClient.createKeyAndCert.response = tt.spmResponse
			spmClient.createKeyAndCert.err = tt.spmError

			got, err := client.CreateKeyAndCert(ctx, tt.request)
			s, ok := status.FromError(err)
			if !ok {
				t.Fatal("unable to extract status code from error")
			}
			if s.Code() != tt.expCode {
				t.Errorf("expected status code: %v, got: %v", tt.expCode, s.Code())
			}
			if got != nil {
				if diff := cmp.Diff(tt.spmResponse, got, protocmp.Transform()); diff != "" {
					t.Errorf("call returned unexpected diff (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestDeriveSymmetricKey(t *testing.T) {
	ctx := context.Background()
	spmClient := &fakeSpmClient{}
	rbClient := &fakeRbClient{}
	conn, err := grpc.DialContext(ctx, "", grpc.WithInsecure(), grpc.WithContextDialer(bufferDialer(t, spmClient, rbClient)))
	if err != nil {
		t.Fatalf("failed to connect to test server: %v", err)
	}
	defer conn.Close()

	client := pbp.NewProvisioningApplianceServiceClient(conn)

	tests := []struct {
		name        string
		request     *pbp.DeriveSymmetricKeyRequest
		expCode     codes.Code
		spmResponse *pbp.DeriveSymmetricKeyResponse
		spmError    error
	}{
		{
			// This is a simple connectivity test. The request and expected
			// response values should be updated if there is additional
			// logic added to the PA service.
			name:        "ok",
			expCode:     codes.OK,
			request:     &pbp.DeriveSymmetricKeyRequest{},
			spmResponse: &pbp.DeriveSymmetricKeyResponse{},
			spmError:    nil,
		},
		{
			// SPM errors are converted to code.Internal.
			name:        "spm_error",
			expCode:     codes.Internal,
			request:     &pbp.DeriveSymmetricKeyRequest{},
			spmResponse: &pbp.DeriveSymmetricKeyResponse{},
			spmError:    status.Errorf(codes.InvalidArgument, "invalid argument"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spmClient.deriveSymmetricKey.response = tt.spmResponse
			spmClient.deriveSymmetricKey.err = tt.spmError

			got, err := client.DeriveSymmetricKey(ctx, tt.request)
			s, ok := status.FromError(err)
			if !ok {
				t.Fatal("unable to extract status code from error")
			}
			if s.Code() != tt.expCode {
				t.Errorf("expected status code: %v, got: %v", tt.expCode, s.Code())
			}
			if got != nil {
				if diff := cmp.Diff(tt.spmResponse, got, protocmp.Transform()); diff != "" {
					t.Errorf("call returned unexpected diff (-want +got):\n%s", diff)
				}
			}
		})
	}
}
