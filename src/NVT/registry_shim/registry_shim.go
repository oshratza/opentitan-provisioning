// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

// Package registry_shim implements the ProvisioningAppliance:RegisterDevice RPC.
package registry_shim

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/golang/protobuf/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	papb "github.com/lowRISC/opentitan-provisioning/src/pa/proto/pa_go_pb"
	di "github.com/lowRISC/opentitan-provisioning/src/proto/device_id_go_pb"
	diu "github.com/lowRISC/opentitan-provisioning/src/proto/device_id_utils"
	rrpb "github.com/lowRISC/opentitan-provisioning/src/proto/registry_record_go_pb"
	nvt_di "github.com/lowRISC/opentitan-provisioning/src/registry_buffer/proto/device_id_go_pb"
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

	conn, err := grpc.Dial(registryBufferAddress, opts, grpc.WithBlock())
	if err != nil {
		log.Printf("In Registry Shim - failed dialing %v", registryBufferAddress)
		return err
	}

	registryClient = pbr.NewRegistryBufferServiceClient(conn)
	return nil
}

// Vendor-specific implementation of RegisterDevice
func RegisterDevice(ctx context.Context, request *papb.RegistrationRequest, endorsement *spmpb.EndorseDataResponse) (*papb.RegistrationResponse, error) {
	log.Printf("In Registry Shim - Received RegisterDevice request with DeviceID: %v", diu.DeviceIdToHexString(request.DeviceData.DeviceId))

	// Check if regitry client is valid.
	if registryClient == nil {
		return nil, status.Errorf(codes.Internal, "RegisterDevice ended with error, PA started without ProxyBuffer")
	}

	// Extract ot.DeviceData to a raw byte buffer.
	deviceDataBytes, err := proto.Marshal(request.DeviceData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal device data: %v", err)
	}

	// Translate/embed ot.DeviceData to ot.RegistryRecord
	Record := rrpb.RegistryRecord{
		DeviceId:      diu.DeviceIdToHexString(request.DeviceData.DeviceId),
		Sku:           request.DeviceData.Sku,
		Version:       0,
		Data:          deviceDataBytes,
		AuthPubkey:    endorsement.Pubkey,
		AuthSignature: endorsement.Signature,
	}

	// Translate/embed ot.RegistryRecord to the registry request.
	rbRequest, err := ConvertPaToRegistryBuffer(request, &Record)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "RegisterDevice returned error: %v", err)
	}

	// Send record to the registry_buffer (the buffering front end of the registry service).
	_, err = registryClient.RegisterDevice(ctx, rbRequest)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "RegisterDevice returned error: %v", err)
	}

	return &papb.RegistrationResponse{}, nil
}

// Converts rrpb.RegistryRecord to pbr.RegistrationRequest (= message type expected by Nuvoton's registry_buffer)
func ConvertPaToRegistryBuffer(request *papb.RegistrationRequest, otRecord *rrpb.RegistryRecord) (*pbr.RegistrationRequest, error) {
	deviceData := request.DeviceData

	// Extract ot.DeviceData to a raw byte buffer.
	otRecordBytes, err := proto.Marshal(otRecord)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal registry record data: %v", err)
	}

	// convert ot.RegistryRecord to nvt_di.DeviceRecord
	deviceRecord := nvt_di.DeviceRecord{
		Sku: deviceData.Sku,
		Id: &nvt_di.DeviceId{
			HardwareOrigin: &nvt_di.HardwareOrigin{
				DeviceType: &nvt_di.DeviceType{
					SiliconCreator:    convertSiliconCreator(deviceData.DeviceId.HardwareOrigin.SiliconCreatorId),
					ProductIdentifier: uint32(deviceData.DeviceId.HardwareOrigin.ProductId),
				},
				DeviceIdentificationNumber: deviceData.DeviceId.HardwareOrigin.DeviceIdentificationNumber,
			},
			SkuSpecific: deviceData.DeviceId.SkuSpecific,
			Crc32:       0, // garbage (legacy field, ignored)
		},
		Data: &nvt_di.DeviceData{
			DeviceIdPub: []*nvt_di.DeviceIdPub{
				{
					Format: nvt_di.DeviceIdPubFormat_DEVICE_ID_PUB_FORMAT_DER,
					Blob:   otRecordBytes, // store the entire ot.RegistryRecord
				},
			},
			Payload:         nil, // legacy field, ignored
			NextOwnerKeys:   nil, // legacy field, ignored
			DeviceLifeCycle: convertDeviceLifeCycle(deviceData.DeviceLifeCycle),
			Metadata: &nvt_di.Metadata{
				State:        convertDeviceState(deviceData.Metadata.RegistrationState),
				CreateTimeMs: deviceData.Metadata.CreateTimeMs,
				UpdateTimeMs: deviceData.Metadata.UpdateTimeMs,
				AteId:        deviceData.Metadata.AteId,
				AteRaw:       deviceData.Metadata.AteRaw,
				Year:         deviceData.Metadata.Year,
				Week:         deviceData.Metadata.Week,
				LotNum:       deviceData.Metadata.LotNum,
				WaferId:      deviceData.Metadata.WaferId,
				X:            deviceData.Metadata.X,
				Y:            deviceData.Metadata.Y,
			},
		},
	}

	registryReq := &pbr.RegistrationRequest{
		DeviceRecord: &deviceRecord,
	}

	return registryReq, nil
}

// Helper function to convert SiliconCreatorId to SiliconCreator enum
func convertSiliconCreator(id di.SiliconCreatorId) nvt_di.SiliconCreator {
	switch id {
	case di.SiliconCreatorId_SILICON_CREATOR_ID_UNSPECIFIED:
		return nvt_di.SiliconCreator_SILICON_CREATOR_UNSPECIFIED
	case di.SiliconCreatorId_SILICON_CREATOR_ID_OPENSOURCE:
		return nvt_di.SiliconCreator_SILICON_CREATOR_TEST // Assuming TEST corresponds to OPENSOURCE
	case di.SiliconCreatorId_SILICON_CREATOR_ID_NUVOTON:
		return nvt_di.SiliconCreator_SILICON_CREATOR_NUVOTON
	default:
		return nvt_di.SiliconCreator_SILICON_CREATOR_UNSPECIFIED
	}
}

// Helper function to convert DeviceLifeCycle enum
func convertDeviceLifeCycle(lifecycle di.DeviceLifeCycle) nvt_di.DeviceLifeCycle {
	switch lifecycle {
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_UNSPECIFIED:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_UNKNOWN
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_RAW:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_RAW
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_TEST_LOCKED:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_TEST_LOCKED
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_TEST_UNLOCKED:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_TEST_UNLOCKED
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_DEV:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_DEV
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_PROD:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_PROD
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_PROD_END:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_PROD_END
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_RMA:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_RMA
	case di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_SCRAP:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_SCRAP
	default:
		return nvt_di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_UNKNOWN
	}
}

// Helper function to convert DeviceRegistrationState to DeviceState enum
func convertDeviceState(state di.DeviceRegistrationState) nvt_di.DeviceState {
	switch state {
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_UNSPECIFIED:
		return nvt_di.DeviceState_DEVICE_STATE_UNKNOWN
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_PROVISIONED:
		return nvt_di.DeviceState_DEVICE_STATE_PROVISIONED
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_PROV_READ:
		return nvt_di.DeviceState_DEVICE_STATE_PROV_READ
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_PROV_REPORT:
		return nvt_di.DeviceState_DEVICE_STATE_PROV_REPORT
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_INVALID:
		return nvt_di.DeviceState_DEVICE_STATE_INVALID
	case di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_REVOKED:
		return nvt_di.DeviceState_DEVICE_STATE_REVOKED
	default:
		return nvt_di.DeviceState_DEVICE_STATE_UNKNOWN
	}
}

// Helper function to marshal di.DeviceData to bytes
func marshalDeviceData(data *di.DeviceData) []byte {
	bytes, err := json.Marshal(data)
	if err != nil {
		log.Fatalf("Failed to marshal device data: %v", err)
	}
	return bytes
}

func example() *papb.RegistrationRequest {
	// Example usage
	return &papb.RegistrationRequest{
		DeviceData: &di.DeviceData{
			Sku: "sku",
			DeviceId: &di.DeviceId{
				HardwareOrigin: &di.HardwareOrigin{
					SiliconCreatorId:           di.SiliconCreatorId_SILICON_CREATOR_ID_NUVOTON,
					ProductId:                  di.ProductId_PRODUCT_ID_EARLGREY_Z1,
					DeviceIdentificationNumber: 1234567890123456789,
					CpReserved:                 0,
				},
				SkuSpecific: []byte("SampleSkuSpecificData"),
			},
			DeviceLifeCycle: di.DeviceLifeCycle_DEVICE_LIFE_CYCLE_PROD,
			Metadata: &di.Metadata{
				RegistrationState: di.DeviceRegistrationState_DEVICE_REGISTRATION_STATE_PROVISIONED,
				CreateTimeMs:      1672531200000,
				UpdateTimeMs:      1672617600000,
				AteId:             "ATE12345",
				AteRaw:            "SampleATEData",
				Year:              8,
				Week:              1,
				LotNum:            42,
				WaferId:           7,
				X:                 10,
				Y:                 20,
			},
			WrappedRmaUnlockToken: []byte("EncryptedRmaToken"),
			PersoTlvData:          []byte("SamplePersonalizationData"),
			PersoFwSha256Hash:     []byte{0x12, 0x34, 0x56, 0x78, 0x9A, 0xBC, 0xDE, 0xF0},
		},
	}
}
