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
	pbr "github.com/lowRISC/opentitan-provisioning/src/proxy_buffer/proto/proxy_buffer_go_pb"
	pbs "github.com/lowRISC/opentitan-provisioning/src/spm/proto/spm_go_pb"
)

const (
	// bufferConnectionSize is the size of the gRPC connection buffer.
	bufferConnectionSize = 2048 * 1024
)

// bufferDialer creates a gRPC buffer connection to an initialized PA service.
// It returns a connection which can then be used to initialize the client
// interface by calling `pbp.NewProvisioningApplianceClient`.
func bufferDialer(t *testing.T, spmClient pbs.SpmServiceClient) func(context.Context, string) (net.Conn, error) {
	listener := bufconn.Listen(bufferConnectionSize)
	server := grpc.NewServer()
	pbp.RegisterProvisioningApplianceServiceServer(server, pa.NewProvisioningApplianceServer(spmClient))
	go func(t *testing.T) {
		if err := server.Serve(listener); err != nil {
			t.Fatal(err)
		}
	}(t)
	return func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}
}

type fakePbClient struct {
	registerDevice      registerDeviceResponse
	batchRegisterDevice batchRegisterDeviceResponse
}

type registerDeviceResponse struct {
	response *pbr.DeviceRegistrationResponse
	err      error
}

type batchRegisterDeviceResponse struct {
	response *pbr.BatchDeviceRegistrationResponse
	err      error
}

func (c *fakePbClient) RegisterDevice(ctx context.Context, request *pbr.DeviceRegistrationRequest, opts ...grpc.CallOption) (*pbr.DeviceRegistrationResponse, error) {
	return c.registerDevice.response, c.registerDevice.err
}

func (c *fakePbClient) BatchRegisterDevice(ctx context.Context, request *pbr.BatchDeviceRegistrationRequest, opts ...grpc.CallOption) (*pbr.BatchDeviceRegistrationResponse, error) {
	return c.batchRegisterDevice.response, c.batchRegisterDevice.err
}

// fakeSpmClient provides a fake client interface to the SPM server. Test
// cases can set the fake responses as part of the test setup.
type fakeSpmClient struct {
	initSession           initSessionResponse
	deriveSymmetricKeys   deriveSymmetricKeysResponse
	getStoredTokens       getStoredTokensResponse
	getCaSubjectKeys      getCaSubjectKeysResponse
	getCaCerts            getCaCertsResponse
	endorseCerts          endorseCertsResponse
	endorseData           endorseDataResponse
	verifyDeviceData      verifyDeviceDataResponse
	getOwnerFwBootMessage getOwnerFwBootMessageResponse
}

type initSessionResponse struct {
	response *pbp.InitSessionResponse
	err      error
}

type deriveSymmetricKeysResponse struct {
	response *pbp.DeriveTokensResponse
	err      error
}

type getStoredTokensResponse struct {
	response *pbp.GetStoredTokensResponse
	err      error
}

type getCaSubjectKeysResponse struct {
	response *pbp.GetCaSubjectKeysResponse
	err      error
}

type getCaCertsResponse struct {
	response *pbp.GetCaCertsResponse
	err      error
}

type endorseCertsResponse struct {
	response *pbp.EndorseCertsResponse
	err      error
}

type endorseDataResponse struct {
	response *pbs.EndorseDataResponse
	err      error
}

type verifyDeviceDataResponse struct {
	response *pbs.VerifyDeviceDataResponse
	err      error
}

type getOwnerFwBootMessageResponse struct {
	response *pbp.GetOwnerFwBootMessageResponse
	err      error
}

func (c *fakeSpmClient) InitSession(ctx context.Context, request *pbp.InitSessionRequest, opts ...grpc.CallOption) (*pbp.InitSessionResponse, error) {
	return c.initSession.response, c.initSession.err
}

func (c *fakeSpmClient) DeriveTokens(ctx context.Context, request *pbp.DeriveTokensRequest, opts ...grpc.CallOption) (*pbp.DeriveTokensResponse, error) {
	return c.deriveSymmetricKeys.response, c.deriveSymmetricKeys.err
}

func (c *fakeSpmClient) GetStoredTokens(ctx context.Context, request *pbp.GetStoredTokensRequest, opts ...grpc.CallOption) (*pbp.GetStoredTokensResponse, error) {
	return c.getStoredTokens.response, c.getStoredTokens.err
}

func (c *fakeSpmClient) GetCaSubjectKeys(ctx context.Context, request *pbp.GetCaSubjectKeysRequest, opts ...grpc.CallOption) (*pbp.GetCaSubjectKeysResponse, error) {
	return c.getCaSubjectKeys.response, c.getCaSubjectKeys.err
}

func (c *fakeSpmClient) GetCaCerts(ctx context.Context, request *pbp.GetCaCertsRequest, opts ...grpc.CallOption) (*pbp.GetCaCertsResponse, error) {
	return c.getCaCerts.response, c.getCaCerts.err
}

func (c *fakeSpmClient) EndorseCerts(ctx context.Context, request *pbp.EndorseCertsRequest, opts ...grpc.CallOption) (*pbp.EndorseCertsResponse, error) {
	return c.endorseCerts.response, c.endorseCerts.err
}

func (c *fakeSpmClient) EndorseData(ctx context.Context, request *pbs.EndorseDataRequest, opts ...grpc.CallOption) (*pbs.EndorseDataResponse, error) {
	return c.endorseData.response, c.endorseData.err
}

func (c *fakeSpmClient) VerifyDeviceData(ctx context.Context, request *pbs.VerifyDeviceDataRequest, opts ...grpc.CallOption) (*pbs.VerifyDeviceDataResponse, error) {
	return c.verifyDeviceData.response, c.verifyDeviceData.err
}

func (c *fakeSpmClient) GetOwnerFwBootMessage(ctx context.Context, request *pbp.GetOwnerFwBootMessageRequest, opts ...grpc.CallOption) (*pbp.GetOwnerFwBootMessageResponse, error) {
	return c.getOwnerFwBootMessage.response, c.getOwnerFwBootMessage.err
}

func TestDeriveSymmetricKey(t *testing.T) {
	ctx := context.Background()
	spmClient := &fakeSpmClient{}
	conn, err := grpc.DialContext(ctx, "", grpc.WithInsecure(), grpc.WithContextDialer(bufferDialer(t, spmClient)))
	if err != nil {
		t.Fatalf("failed to connect to test server: %v", err)
	}
	defer conn.Close()

	client := pbp.NewProvisioningApplianceServiceClient(conn)

	tests := []struct {
		name        string
		request     *pbp.DeriveTokensRequest
		expCode     codes.Code
		spmResponse *pbp.DeriveTokensResponse
		spmError    error
	}{
		{
			// This is a simple connectivity test. The request and expected
			// response values should be updated if there is additional
			// logic added to the PA service.
			name:        "ok",
			expCode:     codes.OK,
			request:     &pbp.DeriveTokensRequest{},
			spmResponse: &pbp.DeriveTokensResponse{},
			spmError:    nil,
		},
		{
			// SPM errors are converted to code.Internal.
			name:        "spm_error",
			expCode:     codes.InvalidArgument,
			request:     &pbp.DeriveTokensRequest{},
			spmResponse: &pbp.DeriveTokensResponse{},
			spmError:    status.Errorf(codes.InvalidArgument, "invalid argument"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spmClient.deriveSymmetricKeys.response = tt.spmResponse
			spmClient.deriveSymmetricKeys.err = tt.spmError

			got, err := client.DeriveTokens(ctx, tt.request)
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

func TestEndorseCerts(t *testing.T) {
	ctx := context.Background()
	spmClient := &fakeSpmClient{}
	conn, err := grpc.DialContext(ctx, "", grpc.WithInsecure(), grpc.WithContextDialer(bufferDialer(t, spmClient)))
	if err != nil {
		t.Fatalf("failed to connect to test server: %v", err)
	}
	defer conn.Close()

	client := pbp.NewProvisioningApplianceServiceClient(conn)

	tests := []struct {
		name        string
		request     *pbp.EndorseCertsRequest
		expCode     codes.Code
		spmResponse *pbp.EndorseCertsResponse
		spmError    error
	}{
		{
			// This is a simple connectivity test. The request and expected
			// response values should be updated if there is additional
			// logic added to the PA service.
			name:        "ok",
			expCode:     codes.OK,
			request:     &pbp.EndorseCertsRequest{},
			spmResponse: &pbp.EndorseCertsResponse{},
			spmError:    nil,
		},
		{
			// SPM errors are converted to code.Internal.
			name:        "spm_error",
			expCode:     codes.InvalidArgument,
			request:     &pbp.EndorseCertsRequest{},
			spmResponse: &pbp.EndorseCertsResponse{},
			spmError:    status.Errorf(codes.InvalidArgument, "invalid argument"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spmClient.deriveSymmetricKeys.response = &pbp.DeriveTokensResponse{}
			spmClient.deriveSymmetricKeys.err = nil

			spmClient.endorseCerts.response = tt.spmResponse
			spmClient.endorseCerts.err = tt.spmError

			got, err := client.EndorseCerts(ctx, tt.request)
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
