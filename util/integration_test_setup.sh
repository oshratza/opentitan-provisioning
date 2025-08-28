#!/bin/bash
# Copyright lowRISC contributors (OpenTitan project).
# Licensed under the Apache License, Version 2.0, see LICENSE for details.
# SPDX-License-Identifier: Apache-2.0

# Parse command line options.
for i in "$@"; do
  case $i in
  # -d option: Activate debug mode, which will not tear down containers if
  # there is a failure so the failure can be inspected.
  -d | --debug)
    export DEBUG="yes"
    shift
    ;;
  --prod)
    export OT_PROV_PROD_EN="yes"
    shift
    ;;
  *)
    echo "Unknown option $i"
    exit 1
    ;;
  esac
done

DEPLOY_ENV="dev"
if [[ -n "${OT_PROV_PROD_EN}" ]]; then
  DEPLOY_ENV="prod"
fi

export OPENTITAN_VAR_DIR=${OPENTITAN_VAR_DIR:-$(pwd)/.ot${DEPLOY_ENV}}

DEPLOYMENT_DIR="${OPENTITAN_VAR_DIR}/config"

# SPM_PID_FILE is used to store the process ID of the SPM server process.
# This is used to send a kill signal to the process when the script exits.
SPM_PID_FILE="/tmp/spm.pid"

# spm_server_try_stop sends a kill signal to the SPM server process if it is
# running. It also waits for the process to terminate and removes the PID file.
# This function is idempotent and can be called multiple times.
spm_server_try_stop() {
  if [ -f "${SPM_PID_FILE}" ]; then
    SPM_PID=$(cat "${SPM_PID_FILE}")
    echo "Stopping SPM server - PID=${SPM_PID}"
    # Check if a process with this PID actually exists before attempting to
    # terminate it.
    ELAPSED=0
    if kill -0 ${SPM_PID} 2>/dev/null; then
      # Kill the entire process group. This is necessary because the bazel
      # command spawns a child process for the server.
      kill -- -${SPM_PID} 2>/dev/null || true
      while kill -0 -- -${SPM_PID} 2>/dev/null; do
        echo "Waiting for SPM server to shut down... ${ELAPSED}s"
        sleep 1
        ELAPSED=$((ELAPSED + 1))
      done
    else
      echo "Process with PID ${SPM_PID} not found. Maybe it was already stopped."
    fi
    rm -f "${SPM_PID_FILE}"
  fi
}

# Unconditionally stop and remove the pod if it exists.
# The --ignore flag is used to suppress errors if the pod does not exist.
spm_server_try_stop
podman pod stop provapp --ignore
podman pod rm provapp --ignore

# Register trap to shutdown containers before exit.
# Teardown containers. This currently does not remove the container volumes.
shutdown_callback() {
  if [ -z "${DEBUG}" ]; then
    echo "Tearing down containers ..."
    podman pod stop provapp
    podman pod rm provapp
  fi

  spm_server_try_stop
}
trap shutdown_callback EXIT

# Build and deploy containers. The ${OT_PROV_PROD_EN} envar is checked
# by `deploy_test_k8_pod.sh`.
./util/containers/deploy_test_k8_pod.sh

. ${DEPLOYMENT_DIR}/env/${DEPLOY_ENV}/spm.env


DEPLOYMENT_BIN_DIR="${OPENTITAN_VAR_DIR}/bin"
BUILD_BIN_DIR="bazel-bin/third_party/lowrisc/ot_fw/orchestrator/runfiles/_main"
if [[ -z "${OT_PROV_ORCHESTRATOR_PATH}" ]]; then
  bazelisk build //third_party/lowrisc/ot_fw:orchestrator_unzip
else
  # Check if the path pointed by OT_PROV_ORCHESTRATOR_PATH points to a valid
  # file.
  if [[ ! -f "${OT_PROV_ORCHESTRATOR_PATH}" ]]; then
    echo "Error: OT_PROV_ORCHESTRATOR_PATH is set to an invalid path: ${OT_PROV_ORCHESTRATOR_PATH}"
    exit 1
  fi
  ORCHESTRATOR_OUT="${OPENTITAN_VAR_DIR}/orchestrator"
  BUILD_BIN_DIR="${ORCHESTRATOR_OUT}/runfiles/_main"
  unzip -q "${OT_PROV_ORCHESTRATOR_PATH}" -d "${ORCHESTRATOR_OUT}"

  # If OT_PROV_ORCHESTRATOR_UNPACK is set, invoke it with the path to
  # ORCHESTRATOR_OUT and BUILD_BIN_DIR.
  if [[ -n "${OT_PROV_ORCHESTRATOR_UNPACK}" ]]; then
    "${OT_PROV_ORCHESTRATOR_UNPACK}" "${ORCHESTRATOR_OUT}" "${DEPLOYMENT_BIN_DIR}"
  fi
fi

# Unpack firmware and OpenOCD binaries.
cp "${BUILD_BIN_DIR}"/sw/device/silicon_creator/manuf/base/*.elf "${DEPLOYMENT_BIN_DIR}"
cp "${BUILD_BIN_DIR}"/sw/device/silicon_creator/manuf/base/*.bin "${DEPLOYMENT_BIN_DIR}"
cp "${BUILD_BIN_DIR}"/sw/device/silicon_creator/manuf/base/binaries/*.bin "${DEPLOYMENT_BIN_DIR}"
cp "${BUILD_BIN_DIR}"/sw/device/silicon_creator/manuf/base/*.img "${DEPLOYMENT_BIN_DIR}"

cp "${BUILD_BIN_DIR}"/third_party/openocd/build_openocd/bin/openocd "${DEPLOYMENT_BIN_DIR}"
chmod +x "${DEPLOYMENT_BIN_DIR}"/openocd


# Spawn the SPM server as a process and store its process ID.
echo "Launching SPM server outside of container"
bazelisk run //src/spm:spm_server -- \
  --enable_tls=true \
  --service_cert="${DEPLOYMENT_DIR}/certs/out/spm-service-cert.pem" \
  --service_key="${DEPLOYMENT_DIR}/certs/out/spm-service-key.pem" \
  --ca_root_certs=${DEPLOYMENT_DIR}/certs/out/ca-cert.pem \
  --port=${OTPROV_PORT_SPM} \
  "--hsm_so=${HSMTOOL_MODULE}" \
  --spm_auth_config="sku_auth.yml" \
  "--spm_config_dir=${DEPLOYMENT_DIR}/spm" &
echo $! > "${SPM_PID_FILE}"
