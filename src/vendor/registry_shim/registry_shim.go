// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

// Package registry_shim implements the ProvisioningAppliance:RegisterDevice RPC.
package registry_shim

import (
	"context"
	"log"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	papb "github.com/lowRISC/opentitan-provisioning/src/pa/proto/pa_go_pb"
	diu "github.com/lowRISC/opentitan-provisioning/src/proto/device_id_utils"
	pbr "github.com/lowRISC/opentitan-provisioning/src/registry_buffer/proto/registry_buffer_go_pb"
	spmpb "github.com/lowRISC/opentitan-provisioning/src/spm/proto/spm_go_pb"
	"github.com/lowRISC/opentitan-provisioning/src/transport/grpconn"
)

var registryClient pbr.RegistryBufferServiceClient

func StartRegistryBuffer(registryBufferAddress string, enableTLS bool, caRootCerts string, serviceCert string, serviceKey string) error {
	log.Printf("In Registry Shim - connecting registry buffer on addrs %v", registryBufferAddress)
	opts := grpc.WithInsecure()
	if enableTLS {
		credentials, err := grpconn.LoadClientCredentials(caRootCerts, serviceCert, serviceKey)
		if err != nil {
			return err
		}
		opts = grpc.WithTransportCredentials(credentials)
	}
	log.Printf("In Registry Shim - 1111111111111111")

	conn, err := grpc.Dial(registryBufferAddress, opts, grpc.WithBlock())
	if err != nil {
		return err
	}
	log.Printf("In Registry Shim - 2222222222222222")

	log.Printf("In Registry Shim - registryClient= %v", registryClient)
	registryClient = pbr.NewRegistryBufferServiceClient(conn)
	log.Printf("In Registry Shim - 3333333333333333")
	log.Printf("In Registry Shim - registryClient= %v", registryClient)

	return nil
}

func RegisterDevice(ctx context.Context, spmClient spmpb.SpmServiceClient, request *papb.RegistrationRequest) (*papb.RegistrationResponse, error) {
	log.Printf("In Registry Shim - Received RegisterDevice request with DeviceID: %v", diu.DeviceIdToHexString(request.DeviceData.DeviceId))

	// Vendor-specific implementation of RegisterDevice call goes here.
	return nil, status.Errorf(codes.Unimplemented, "Vendor specific RegisterDevice RPC not implemented.")
}
