#!/bin/bash
# Copyright lowRISC contributors (OpenTitan project).
# Licensed under the Apache License, Version 2.0, see LICENSE for details.
# SPDX-License-Identifier: Apache-2.0

set -e

# Build and deploy the provisioning infrastructure.
source util/integration_test_setup.sh

# Run the PA loadtest.
echo "Running PA loadtest ..."
bazelisk run //src/pa:loadtest -- \
   --enable_tls=true \
   --client_cert="${DEPLOYMENT_DIR}/certs/out/ate-client-cert.pem" \
   --client_key="${DEPLOYMENT_DIR}/certs/out/ate-client-key.pem" \
   --ca_root_certs=${DEPLOYMENT_DIR}/certs/out/ca-cert.pem \
   --pa_address="${OTPROV_DNS_PA}:${OTPROV_PORT_PA}" \
   --sku_auth="test_password" \
   --parallel_clients=2 \
   --total_calls_per_method=4
echo "Done."

# Run the CP and FT flows (default to hyper340 since that is installed in CI).
FPGA="${FPGA:-hyper340}"
if [[ "$FPGA" == "hyper340" ]]; then
  BIN_DEVICE="cw340"
else
  BIN_DEVICE="hyper310"
fi

echo "Running CP FPGA test flow ..."
bazelisk run //src/ate/test_programs:cp -- \
  --enable_mtls=true \
  --client_cert="${DEPLOYMENT_DIR}/certs/out/ate-client-cert.pem" \
  --client_key="${DEPLOYMENT_DIR}/certs/out/ate-client-key.pem" \
  --ca_root_certs=${DEPLOYMENT_DIR}/certs/out/ca-cert.pem \
  --pa_socket="ipv4:${OTPROV_IP_PA}:${OTPROV_PORT_PA}" \
  --sku="sival" \
  --sku_auth_pw="test_password" \
  --fpga="${FPGA}" \
  --bitstream="$(pwd)/third_party/lowrisc/ot_bitstreams/cp_${FPGA}.bit" \
  --cp_sram_elf="${DEPLOYMENT_BIN_DIR}/sram_cp_provision_fpga_${BIN_DEVICE}_rom_with_fake_keys.elf" \
  --openocd="${DEPLOYMENT_BIN_DIR}/openocd"
echo "Done."

echo "Running FT FPGA test flow ..."
bazelisk run //src/ate/test_programs:ft -- \
  --enable_mtls=true \
  --client_cert="${DEPLOYMENT_DIR}/certs/out/ate-client-cert.pem" \
  --client_key="${DEPLOYMENT_DIR}/certs/out/ate-client-key.pem" \
  --ca_root_certs=${DEPLOYMENT_DIR}/certs/out/ca-cert.pem \
  --pa_socket="ipv4:${OTPROV_IP_PA}:${OTPROV_PORT_PA}" \
  --sku="sival" \
  --sku_auth_pw="test_password" \
  --fpga="${FPGA}" \
  --ft_individualization_elf="${DEPLOYMENT_BIN_DIR}/sram_ft_individualize_sival_ate_fpga_${BIN_DEVICE}_rom_with_fake_keys.elf" \
  --ft_personalize_bin="${DEPLOYMENT_BIN_DIR}/ft_personalize_sival_fpga_${BIN_DEVICE}_rom_with_fake_keys.prod_key_0.prod_key_0.signed.bin" \
  --openocd="${DEPLOYMENT_BIN_DIR}/openocd"
echo "Done."
