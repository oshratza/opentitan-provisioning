// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

#include "src/ate/ate_client.h"

#include <grpcpp/grpcpp.h>
#include <grpcpp/security/credentials.h>

#include <iostream>
#include <memory>
#include <string>

#include "absl/log/log.h"
#include "absl/memory/memory.h"
#include "absl/status/statusor.h"
#include "src/pa/proto/pa.grpc.pb.h"
#include "src/transport/service_credentials.h"

namespace provisioning {
namespace ate {
namespace {
using grpc::Channel;
using grpc::ClientContext;
using grpc::Status;
using pa::CloseSessionRequest;
using pa::CloseSessionResponse;
using pa::DeriveTokensRequest;
using pa::DeriveTokensResponse;
using pa::EndorseCertsRequest;
using pa::EndorseCertsResponse;
using pa::GetCaCertsRequest;
using pa::GetCaCertsResponse;
using pa::GetCaSubjectKeysRequest;
using pa::GetCaSubjectKeysResponse;
using pa::GetOwnerFwBootMessageRequest;
using pa::GetOwnerFwBootMessageResponse;
using pa::InitSessionRequest;
using pa::InitSessionResponse;
using pa::ProvisioningApplianceService;
using pa::RegistrationRequest;
using pa::RegistrationResponse;
using provisioning::transport::ServiceCredentials;

// Creates mTLS and per call channel credentials based on configuration
// `options`.
std::shared_ptr<grpc::ChannelCredentials> BuildCredentials(
    const AteClient::Options& options) {
  grpc::SslCredentialsOptions credentials_opts;
  credentials_opts.pem_root_certs = options.pem_root_certs;
  credentials_opts.pem_private_key = options.pem_private_key;
  credentials_opts.pem_cert_chain = options.pem_cert_chain;

  auto call_credentials = grpc::MetadataCredentialsFromPlugin(
      std::unique_ptr<grpc::MetadataCredentialsPlugin>(
          new ServiceCredentials(options.sku_tokens)));

  return grpc::CompositeChannelCredentials(
      grpc::SslCredentials(credentials_opts), call_credentials);
}
}  // namespace

// By explicitly defining the new and delete operators for the AteClient class
// and implementing them in the same compilation unit (the DLL), we ensure
// that the memory for AteClient objects is allocated and deallocated on the
// same heap.
//
// On Windows, a DLL and the executable that loads it can have different C++
// runtime heaps. If an object is allocated on one heap (e.g., by a call
// from the .exe that results in a `new` inside the DLL) and deallocated on
// another (e.g., by a `delete` call in the DLL that might resolve to the
// .exe's runtime), it can lead to heap corruption and access violation
// errors.
//
// These overloads ensure that `new AteClient` and `delete AteClient` always
// use the memory management functions from the C++ runtime linked with this
// DLL, preventing such issues.
void* AteClient::operator new(size_t size) {
  // Forward to the global new operator from the DLL's runtime.
  return ::operator new(size);
}

void AteClient::operator delete(void* ptr) {
  // Forward to the global delete operator from the DLL's runtime.
  ::operator delete(ptr);
}

// Instantiates a client
std::unique_ptr<AteClient> AteClient::Create(AteClient::Options options) {
  // establish a grpc channel between the client (test program) and the targeted
  // provisioning appliance server:
  // 1. set the grpc channel properties (insecured by default, authenticated and
  // encrypted if specified in options.enable_mtls parameter)
  auto credentials = grpc::InsecureChannelCredentials();
  if (options.enable_mtls) {
    credentials = BuildCredentials(options);
  }
  // 2. create the grpc channel between the client and the targeted server
  grpc::ChannelArguments args;
  if (!options.load_balancing_policy.empty()) {
    args.SetLoadBalancingPolicyName(options.load_balancing_policy);
  }
  auto channel =
      grpc::CreateCustomChannel(options.pa_target, credentials, args);
  auto ate = absl::make_unique<AteClient>(channel);

  return ate;
}

Status AteClient::InitSession(const std::string& sku,
                              const std::string& sku_auth) {
  LOG(INFO) << "AteClient::InitSession, sku: " << sku;
  Status result;
  Sku = sku;

  InitSessionRequest request;
  request.set_sku(sku);
  request.set_sku_auth(sku_auth);

  InitSessionResponse response;
  ClientContext context;

  result = stub_->InitSession(&context, request, &response);
  if (!result.ok()) {
    return result;
  }
  sku_session_token_ = response.sku_session_token();
  return Status::OK;
}

Status AteClient::CloseSession() {
  LOG(INFO) << "AteClient::CloseSession";
  Status result;
  CloseSessionRequest request;
  CloseSessionResponse response;
  ClientContext context;

  result = stub_->CloseSession(&context, request, &response);
  if (!result.ok()) {
    return result;
  }
  return Status::OK;
}

Status AteClient::EndorseCerts(EndorseCertsRequest& request,
                               EndorseCertsResponse* reply) {
  LOG(INFO) << "AteClient::EndorseCerts";

  // Context for the client (It could be used to convey extra information to
  // the server and/or tweak certain RPC behaviors).
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);

  // The actual RPC - call the server's EndorseCerts method.
  return stub_->EndorseCerts(&context, request, reply);
}

Status AteClient::DeriveTokens(DeriveTokensRequest& request,
                               DeriveTokensResponse* reply) {
  LOG(INFO) << "AteClient::DeriveTokens";

  // Context for the client (It could be used to convey extra information to
  // the server and/or tweak certain RPC behaviors).
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);

  // The actual RPC - call the server's DeriveTokens method.
  return stub_->DeriveTokens(&context, request, reply);
}

Status AteClient::GetCaSubjectKeys(GetCaSubjectKeysRequest& request,
                                   GetCaSubjectKeysResponse* reply) {
  LOG(INFO) << "AteClient::GetCaSubjectKeys";

  // Context for the client (It could be used to convey extra information to
  // the server and/or tweak certain RPC behaviors).
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);

  // The actual RPC - call the server's DeriveTokens method.
  return stub_->GetCaSubjectKeys(&context, request, reply);
}

Status AteClient::GetCaCerts(GetCaCertsRequest& request,
                             GetCaCertsResponse* reply) {
  LOG(INFO) << "AteClient::GetCaCerts";

  // Context for the client (It could be used to convey extra information to
  // the server and/or tweak certain RPC behaviors).
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);

  // The actual RPC - call the server's DeriveTokens method.
  return stub_->GetCaCerts(&context, request, reply);
}

Status AteClient::GetOwnerFwBootMessage(GetOwnerFwBootMessageRequest& request,
                                        GetOwnerFwBootMessageResponse* reply) {
  LOG(INFO) << "AteClient::GetOwnerFwBootMessage";
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);
  return stub_->GetOwnerFwBootMessage(&context, request, reply);
}

Status AteClient::RegisterDevice(RegistrationRequest& request,
                                 RegistrationResponse* reply) {
  LOG(INFO) << "AteClient::RegisterDevice";

  // Context for the client (It could be used to convey extra information to
  // the server and/or tweak certain RPC behaviors).
  ClientContext context;
  context.AddMetadata("authorization", sku_session_token_);

  // The actual RPC - call the server's RegisterDevice method.
  return stub_->RegisterDevice(&context, request, reply);
}

// overloads operator<< for AteClient::Options objects printouts
std::ostream& operator<<(std::ostream& os, const AteClient::Options& options) {
  // write obj to stream
  os << std::endl << "options.pa_target = " << options.pa_target << std::endl;
  os << "options.load_balancing_policy = " << options.load_balancing_policy
     << std::endl;
  os << "options.enable_mtls = " << options.enable_mtls << std::endl;
  os << "options.pem_cert_chain = " << options.pem_cert_chain << std::endl;
  os << "options.pem_private_key = " << options.pem_private_key << std::endl;
  os << "options.pem_root_certs = " << options.pem_root_certs << std::endl;
  return os;
}

}  // namespace ate
}  // namespace provisioning
