// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

#include <grpcpp/grpcpp.h>

#include <fstream>
#include <iomanip>
#include <iostream>
#include <memory>
#include <sstream>
#include <string>
#include <unordered_map>

#include "absl/flags/flag.h"
#include "absl/flags/parse.h"
#include "absl/flags/usage_config.h"
#include "absl/log/log.h"
#include "absl/status/status.h"
#include "absl/status/statusor.h"
#include "absl/strings/str_format.h"
#include "absl/strings/str_replace.h"
#include "external/lowrisc_opentitan/sw/device/lib/dif/dif_lc_ctrl.h"
#include "src/ate/ate_api.h"
#include "src/ate/test_programs/dut_lib/dut_lib.h"
#include "src/pa/proto/pa.grpc.pb.h"
#include "src/pa/proto/pa.pb.h"
#include "src/version/version.h"

/**
 * DUT configuration flags.
 */
ABSL_FLAG(std::string, fpga, "", "FPGA platform to use.");
ABSL_FLAG(std::string, openocd, "", "OpenOCD binary path.");
ABSL_FLAG(std::string, ft_individualization_elf, "",
          "FT Individualization ELF (device binary).");
ABSL_FLAG(std::string, ft_personalize_bin, "",
          "FT Personalize Binary (device binary).");
ABSL_FLAG(std::string, ft_fw_bundle_bin, "",
          "FT Personalize / Transport image bundle (device binary).");

/**
 * PA configuration flags.
 */
ABSL_FLAG(std::string, pa_target, "",
          "Endpoint address in gRPC name-syntax format, including port "
          "number. For example: \"localhost:5000\", "
          "\"ipv4:127.0.0.1:5000,127.0.0.2:5000\", or "
          "\"ipv6:[::1]:5000,[::1]:5001\".");
ABSL_FLAG(std::string, load_balancing_policy, "",
          "gRPC load balancing policy. If not set, it will be selected by "
          "the gRPC library. For example: \"round_robin\" or "
          "\"pick_first\".");
ABSL_FLAG(std::string, sku, "", "SKU string to initialize the PA session.");
ABSL_FLAG(std::string, sku_auth_pw, "",
          "SKU authorization password string to initialize the PA session.");

/**
 * mTLS configuration flags.
 */
ABSL_FLAG(bool, enable_mtls, false, "Enable mTLS secure channel.");
ABSL_FLAG(std::string, client_key, "",
          "File path to the PEM encoding of the client's private key.");
ABSL_FLAG(std::string, client_cert, "",
          "File path to the PEM encoding of the  client's certificate chain.");
ABSL_FLAG(std::string, ca_root_certs, "",
          "File path to the PEM encoding of the server root certificates.");

namespace {
using provisioning::VersionFormatted;
using provisioning::test_programs::DutLib;

absl::StatusOr<ate_client_ptr> AteClientNew(void) {
  client_options_t options;

  std::string pa_target = absl::GetFlag(FLAGS_pa_target);
  if (pa_target.empty()) {
    return absl::InvalidArgumentError(
        "--pa_target not set. This is a required argument.");
  }
  options.pa_target = pa_target.c_str();
  options.enable_mtls = absl::GetFlag(FLAGS_enable_mtls);

  std::string lb_policy = absl::GetFlag(FLAGS_load_balancing_policy);
  options.load_balancing_policy = lb_policy.c_str();

  std::string pem_private_key = absl::GetFlag(FLAGS_client_key);
  std::string pem_cert_chain = absl::GetFlag(FLAGS_client_cert);
  std::string pem_root_certs = absl::GetFlag(FLAGS_ca_root_certs);

  if (options.enable_mtls) {
    if (pem_private_key.empty() || pem_cert_chain.empty() ||
        pem_root_certs.empty()) {
      return absl::InvalidArgumentError(
          "--client_key, --client_cert, and --ca_root_certs are required "
          "arguments when --enable_mtls is set.");
    }
    options.pem_private_key = pem_private_key.c_str();
    options.pem_cert_chain = pem_cert_chain.c_str();
    options.pem_root_certs = pem_root_certs.c_str();
  }

  ate_client_ptr ate_client;
  if (CreateClient(&ate_client, &options) != 0) {
    return absl::InternalError("Failed to create ATE client.");
  }
  if (ate_client == nullptr) {
    return absl::InternalError("Failed to create ATE client.");
  }
  return ate_client;
}

absl::StatusOr<std::string> ValidateFilePathInput(std::string path) {
  std::ifstream file_stream(path);
  if (file_stream.good()) {
    return path;
  }
  return absl::InvalidArgumentError(
      absl::StrCat("Unable to open file: \"", path, "\""));
}

bool SetDiversificationString(uint8_t *diversifier, const std::string &str) {
  if (str.size() > kDiversificationStringSize) {
    return false;
  }
  memcpy(diversifier, str.data(), str.size());
  memset(diversifier + str.size(), 0, kDiversificationStringSize - str.size());
  return true;
}
}  // namespace

int main(int argc, char **argv) {
  // Parse cmd line args.
  absl::FlagsUsageConfig config;
  absl::SetFlagsUsageConfig(config);
  absl::ParseCommandLine(argc, argv);

  // Set version string.
  config.version_string = &VersionFormatted;
  LOG(INFO) << VersionFormatted();

  // Validate OpenOCD path.
  auto openocd_result = ValidateFilePathInput(absl::GetFlag(FLAGS_openocd));
  if (!openocd_result.ok()) {
    LOG(ERROR) << openocd_result.status().message() << std::endl;
    return -1;
  }
  std::string openocd_path = openocd_result.value();
  // Validate FT firmware binary paths.
  auto ft_individ_elf_result =
      ValidateFilePathInput(absl::GetFlag(FLAGS_ft_individualization_elf));
  if (!ft_individ_elf_result.ok()) {
    LOG(ERROR) << ft_individ_elf_result.status().message() << std::endl;
    return -1;
  }
  std::string ft_individ_elf_path = ft_individ_elf_result.value();
  auto ft_perso_bin_result =
      ValidateFilePathInput(absl::GetFlag(FLAGS_ft_personalize_bin));
  if (!ft_perso_bin_result.ok()) {
    LOG(ERROR) << ft_perso_bin_result.status().message() << std::endl;
    return -1;
  }
  std::string ft_perso_bin_path = ft_perso_bin_result.value();
  auto ft_fw_bundle_result =
      ValidateFilePathInput(absl::GetFlag(FLAGS_ft_fw_bundle_bin));
  if (!ft_fw_bundle_result.ok()) {
    LOG(ERROR) << ft_fw_bundle_result.status().message() << std::endl;
    return -1;
  }
  std::string ft_fw_bundle_path = ft_fw_bundle_result.value();

  // Instantiate an ATE client (gateway to PA).
  auto ate_client_result = AteClientNew();
  if (!ate_client_result.ok()) {
    LOG(ERROR) << ate_client_result.status().message() << std::endl;
    return -1;
  }
  ate_client_ptr ate_client = ate_client_result.value();

  // Init session with PA.
  if (InitSession(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                  absl::GetFlag(FLAGS_sku_auth_pw).c_str()) != 0) {
    LOG(ERROR) << "InitSession with PA failed.";
    return -1;
  }

  // Init session with FPGA DUT.
  //
  // Note: we do not reload the bitstream as the CP test program should be run
  // before running this test program.
  auto dut = DutLib::Create(absl::GetFlag(FLAGS_fpga));

  // Regenerate the test tokens.
  derive_token_params_t test_tokens_params[] = {
      {
          // Test Unlock Token
          .seed = kTokenSeedSecurityLow,
          .type = kTokenTypeRaw,
          .size = kTokenSize128,
          .diversifier = {0},
      },
      {
          // Test Exit Token
          .seed = kTokenSeedSecurityLow,
          .type = kTokenTypeRaw,
          .size = kTokenSize128,
          .diversifier = {0},
      },
  };
  if (!SetDiversificationString(test_tokens_params[0].diversifier,
                                "test_unlock")) {
    LOG(ERROR) << "Failed to set diversifier for test_unlock.";
    return -1;
  }
  if (!SetDiversificationString(test_tokens_params[1].diversifier,
                                "test_exit")) {
    LOG(ERROR) << "Failed to set diversifier for test_exit.";
    return -1;
  }
  constexpr size_t kNumTokens = 2;
  token_t tokens[kNumTokens];
  if (DeriveTokens(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                   /*count=*/kNumTokens, test_tokens_params, tokens) != 0) {
    LOG(ERROR) << "DeriveTokens failed.";
    return -1;
  }

  // Generate the RMA unlock token hash.
  generate_token_params_t rma_token_params = {
      .type = kTokenTypeHashedLcToken,
      .size = kTokenSize128,
      .diversifier = {0},
  };
  if (!SetDiversificationString(rma_token_params.diversifier, "rma")) {
    LOG(ERROR) << "Failed to set diversifier for RMA.";
    return -1;
  }
  token_t rma_token;
  wrapped_seed_t wrapped_rma_token_seed;
  if (GenerateTokens(ate_client, absl::GetFlag(FLAGS_sku).c_str(), /*count=*/1,
                     &rma_token_params, &rma_token,
                     &wrapped_rma_token_seed) != 0) {
    LOG(ERROR) << "GenerateTokens failed.";
    return -1;
  }
  dut_spi_frame_t rma_token_spi_frame;
  if (RmaTokenToJson(&rma_token, &rma_token_spi_frame, /*skip_crc=*/false) !=
      0) {
    LOG(ERROR) << "RmaTokenToJson failed.";
    return -1;
  }

  // Generate CA subject keys.
  constexpr size_t kNumIcas = 2;
  const char *kIcaCertLabels[] = {
      "UDS",
      "EXT",
  };
  ca_subject_key_t key_ids[kNumIcas];
  if (GetCaSubjectKeys(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                       /*count=*/kNumIcas, kIcaCertLabels, key_ids) != 0) {
    LOG(ERROR) << "GetCaSubjectKeys failed.";
    return -1;
  }
  const ca_subject_key_t *kDiceCaSk = &key_ids[0];
  const ca_subject_key_t *kExtCaSk = &key_ids[1];
  dut_spi_frame_t ca_key_ids_spi_frame;
  if (CaSubjectKeysToJson(kDiceCaSk, kExtCaSk, &ca_key_ids_spi_frame) != 0) {
    LOG(ERROR) << "CaSubjectKeysToJson failed.";
    return -1;
  }

  // Unlock the chip and run the individualization firmware.
  dut->DutLcTransition(openocd_path, tokens[0].data, kTokenSize128,
                       kDifLcCtrlStateTestUnlocked1);
  dut->DutLoadSramElf(openocd_path, ft_individ_elf_path,
                      /*wait_for_done=*/true,
                      /*timeout_ms=*/1000);

  // Transition to mission mode and start running the personalization firmware.
  dut->DutLcTransition(openocd_path, tokens[1].data, kTokenSize128,
                       kDifLcCtrlStateProd);
  dut->DutBootstrap(ft_perso_bin_path);
  dut->DutConsoleWaitForRx("Bootstrap requested.", /*timeout_ms=*/1000);
  dut->DutBootstrap(ft_fw_bundle_path);
  dut->DutConsoleTx("Waiting For RMA Unlock Token Hash ...",
                    rma_token_spi_frame.payload, kDutRxSpiFrameSizeInBytes,
                    /*timeout_ms=*/1000);
  dut->DutConsoleTx("Waiting for certificate inputs ...",
                    ca_key_ids_spi_frame.payload, kDutRxSpiFrameSizeInBytes,
                    /*timeout_ms=*/1000);

  // Receive the TBS certs and other provisioning data from the DUT.
  constexpr size_t kMaxNumPbSpiFrames = 10;
  dut_spi_frame_t pb_spi_frames[kMaxNumPbSpiFrames];
  size_t num_pb_spi_frames = kMaxNumPbSpiFrames;
  dut->DutConsoleRx("Exporting TBS certificates ...", pb_spi_frames,
                    &num_pb_spi_frames,
                    /*skip_crc_check=*/true,
                    /*quiet=*/true,
                    /*timeout_ms=*/10000);
  perso_blob_t perso_blob_from_dut = {0};
  if (PersoBlobFromJson(pb_spi_frames, num_pb_spi_frames,
                        &perso_blob_from_dut)) {
    LOG(ERROR) << "PersoBlobFromJson failed.";
    return -1;
  }

  // Unpack the provisioning data (TBS certs, device ID, dev seeds, etc.) from
  // the perso blob.
  device_id_bytes_t device_id;
  endorse_cert_signature_t tbs_was_hmac = {.raw = {0}};
  sha256_hash_t perso_fw_hash = {.raw = {0}};
  constexpr size_t kMaxNumCerts = 10;
  size_t num_tbs_certs = kMaxNumCerts;
  endorse_cert_request_t tbs_certs[kMaxNumCerts];
  size_t num_dut_endorsed_certs = kMaxNumCerts;
  endorse_cert_response_t dut_endorsed_certs[kMaxNumCerts];
  constexpr size_t kMaxSeeds = 5;
  seed_t seeds[kMaxSeeds];
  size_t num_seeds = kMaxSeeds;
  if (UnpackPersoBlob(&perso_blob_from_dut, &device_id, &tbs_was_hmac,
                      &perso_fw_hash, tbs_certs, &num_tbs_certs,
                      dut_endorsed_certs, &num_dut_endorsed_certs, seeds,
                      &num_seeds) != 0) {
    LOG(ERROR) << "Failed to unpack the perso blob from the DUT.";
    return -1;
  }

  // Log the device ID and number of TBS certs to be endorsed.
  uint32_t *device_id_words = reinterpret_cast<uint32_t *>(device_id.raw);
  LOG(INFO) << absl::StrFormat("Device ID: 0x%08x%08x%08x%08x%08x%08x%08x%08x",
                               device_id_words[7], device_id_words[6],
                               device_id_words[5], device_id_words[4],
                               device_id_words[3], device_id_words[2],
                               device_id_words[1], device_id_words[0]);
  LOG(INFO) << "Number of TBS certs extracted: " << num_tbs_certs;
  LOG(INFO) << "Number of (complete) certs extracted: "
            << num_dut_endorsed_certs;

  // Endorse the TBS certs with the PA/SPM.
  // TODO(timothytrippel): Set diversifier to "was" || CP device ID.
  diversifier_bytes_t was_diversifier = {0};
  if (!SetDiversificationString(was_diversifier.raw, "was")) {
    LOG(ERROR) << "Failed to set diversifier for WAS.";
    return -1;
  }
  endorse_cert_response_t pa_endorsed_certs[num_tbs_certs];
  if (EndorseCerts(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                   &was_diversifier, &tbs_was_hmac, num_tbs_certs, tbs_certs,
                   pa_endorsed_certs) != 0) {
    LOG(ERROR) << "Failed to endorse certs.";
    return -1;
  }

  // Send the endorsed certs back to the device.
  perso_blob_t perso_blob_from_ate = {0};
  constexpr size_t kNumPersoBlobMaxNumSpiFrames = 50;
  dut_spi_frame_t perso_blob_from_ate_spi_frames[kNumPersoBlobMaxNumSpiFrames];
  size_t num_perso_blob_spi_frames = kNumPersoBlobMaxNumSpiFrames;
  if (PackPersoBlob(num_tbs_certs, pa_endorsed_certs, &perso_blob_from_ate) !=
      0) {
    LOG(ERROR) << "Failed to repack the perso blob.";
    return -1;
  }
  if (PersoBlobToJson(&perso_blob_from_ate, perso_blob_from_ate_spi_frames,
                      &num_perso_blob_spi_frames) != 0) {
    LOG(ERROR) << "PersoBlobToJson failed.";
    return -1;
  }
  const char *perso_blob_sync_msg = "Importing endorsed certificates ...";
  const char *empty_sync_msg = "";
  for (size_t i = 0; i < num_perso_blob_spi_frames; ++i) {
    if (i == 0) {
      dut->DutConsoleTx(perso_blob_sync_msg,
                        perso_blob_from_ate_spi_frames[i].payload,
                        kDutRxSpiFrameSizeInBytes,
                        /*timeout_ms=*/1000);
    } else {
      dut->DutConsoleTx(empty_sync_msg,
                        perso_blob_from_ate_spi_frames[i].payload,
                        kDutRxSpiFrameSizeInBytes,
                        /*timeout_ms=*/1000);
    }
  }

  // Capture the hash of all installed certificates.
  dut_spi_frame_t cert_hash_spi_frame;
  size_t num_cert_hash_spi_frames = 1;
  dut->DutConsoleRx("Finished importing certificates.", &cert_hash_spi_frame,
                    &num_cert_hash_spi_frames,
                    /*skip_crc_check=*/true,
                    /*quiet=*/false,
                    /*timeout_ms=*/1000);
  sha256_hash_t hash_of_all_certs = {0};
  if (Sha256HashFromJson(&cert_hash_spi_frame, &hash_of_all_certs)) {
    LOG(ERROR) << "Sha256HashFromJson failed.";
    return -1;
  }

  // Register the device.
  // Arbitrary metadata for testing.
  metadata_t dut_metadata = {
      .year = 0,
      .week = 10,
      .lot_num = 123,
      .wafer_id = 456,
      .x = 25,
      .y = 52,
  };
  perso_blob_t perso_blob_for_registry;
  if (PackRegistryPersoTlvData(dut_endorsed_certs, num_dut_endorsed_certs,
                               pa_endorsed_certs, num_tbs_certs, seeds,
                               num_seeds, &perso_blob_for_registry) != 0) {
    LOG(ERROR) << "PackRegistryPersoTlvData failed.";
    return -1;
  }
  // TODO(timothytrippel): add helper function to translate kDifLcCtrlStateProd
  // to kDeviceLifeCycleProd
  if (RegisterDevice(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                     reinterpret_cast<const device_id_t *>(&device_id),
                     kDeviceLifeCycleProd, &dut_metadata,
                     &wrapped_rma_token_seed, &perso_blob_for_registry,
                     &perso_fw_hash, &hash_of_all_certs, nullptr, 0) != 0) {
    LOG(ERROR) << "RegisterDevice failed.";
    return -1;
  }

  // TODO(timothytrippel): check the hash of all certificates written to flash.

  // Wait for the DUT to boot the transport image successfully (i.e., the
  // ROM_EXT + Owner FW payload).
  dut->DutConsoleWaitForRx("Personalization done.\n", /*timeout_ms=*/1000);
  constexpr size_t kMaxBootMessageStringSize = 50;
  char boot_msg_cstr[kMaxBootMessageStringSize] = {0};
  if (GetOwnerFwBootMessage(ate_client, absl::GetFlag(FLAGS_sku).c_str(),
                            boot_msg_cstr, kMaxBootMessageStringSize) != 0) {
    LOG(ERROR) << "GetOwnerFwBootMessage failed.";
    return -1;
  }
  dut->DutCheckTransportImgBoot(boot_msg_cstr, /*timeout_ms=*/5000);

  // Close session with PA.
  if (CloseSession(ate_client) != 0) {
    LOG(ERROR) << "CloseSession with PA failed.";
    return -1;
  }
  DestroyClient(ate_client);
  return 0;
}
