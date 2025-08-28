// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

#include "src/ate/ate_perso_blob.h"

#include <stddef.h>
#include <stdint.h>
#include <string.h>

#include "absl/log/log.h"
#include "src/ate/ate_api.h"

namespace {

// Helper function to extract a certificate from a perso blob.
int ExtractCertObject(const uint8_t* buf, size_t buf_size,
                      perso_tlv_cert_obj_t* cert_obj) {
  if (buf == nullptr || cert_obj == nullptr) {
    LOG(ERROR) << "Invalid input buffer or cert_obj pointer";
    return -1;
  }
  if (buf_size < sizeof(perso_tlv_object_header_t)) {
    LOG(ERROR) << "Buffer too small for object header";
    return -1;
  }

  const perso_tlv_object_header_t* objh =
      reinterpret_cast<const perso_tlv_object_header_t*>(buf);

  uint16_t obj_size;
  PERSO_TLV_GET_FIELD(Objh, Size, *objh, &obj_size);
  if (obj_size == 0 || obj_size > buf_size) {
    LOG(ERROR) << "Invalid object size: " << obj_size
               << ", buffer size: " << buf_size;
    return -1;
  }

  uint16_t obj_type;
  PERSO_TLV_GET_FIELD(Objh, Type, *objh, &obj_type);
  if (obj_type != kPersoObjectTypeX509Tbs &&
      obj_type != kPersoObjectTypeX509Cert &&
      obj_type != kPersoObjectTypeCwtCert) {
    LOG(ERROR) << "Invalid object type: " << obj_type
               << ", expected X509 TBS or (full) cert";
    return -1;
  }

  buf += sizeof(perso_tlv_object_header_t);
  buf_size -= sizeof(perso_tlv_object_header_t);

  const perso_tlv_cert_header_t* crth =
      reinterpret_cast<const perso_tlv_cert_header_t*>(buf);

  if (buf_size < sizeof(perso_tlv_cert_header_t)) {
    LOG(ERROR) << "Buffer too small for certificate header";
    return -1;
  }

  uint16_t name_len;
  PERSO_TLV_GET_FIELD(Crth, NameSize, *crth, &name_len);

  uint16_t cert_body_size;
  PERSO_TLV_GET_FIELD(Crth, Size, *crth, &cert_body_size);

  buf += sizeof(perso_tlv_cert_header_t);
  buf_size -= sizeof(perso_tlv_cert_header_t);

  if (buf_size < name_len) {
    LOG(ERROR) << "Buffer too small for certificate name: " << name_len
               << ", available: " << buf_size;
    return -1;
  }

  memcpy(cert_obj->name, buf, name_len);
  cert_obj->name[name_len] = '\0';

  buf += name_len;
  buf_size -= name_len;

  cert_body_size = cert_body_size - name_len - sizeof(perso_tlv_cert_header_t);
  if (cert_body_size > buf_size) {
    LOG(ERROR) << "Certificate body size exceeds available buffer size: "
               << cert_body_size << " > " << buf_size;
    return -1;
  }
  cert_obj->cert_body_size = cert_body_size;
  cert_obj->cert_body_p = reinterpret_cast<const char*>(buf);

  return 0;
}

// Helper function to extract a TBS certificate from a perso blob.
int PackX509TbsCertStruct(const perso_tlv_cert_obj_t* cert_obj,
                          endorse_cert_request_t* tbs_cert) {
  if (cert_obj == nullptr || tbs_cert == nullptr) {
    LOG(ERROR) << "Invalid cert_obj or tbs_cert object pointer.";
    return -1;
  }

  // Copy the certificate body.
  if (cert_obj->cert_body_size > kCertificateMaxSize) {
    LOG(ERROR) << "TBS certificate body size exceeds maximum: "
               << cert_obj->cert_body_size << " > " << kCertificateMaxSize;
    return -1;
  }
  memset(tbs_cert->tbs, 0, sizeof(tbs_cert->tbs));
  memcpy(tbs_cert->tbs, cert_obj->cert_body_p, cert_obj->cert_body_size);
  tbs_cert->tbs_size = cert_obj->cert_body_size;

  // Copy the key label.
  size_t key_label_size = strlen(cert_obj->name);
  if (key_label_size > kCertificateKeyLabelMaxSize) {
    LOG(ERROR) << "Key label size exceeds maximum: " << key_label_size << " > "
               << kCertificateKeyLabelMaxSize;
    return -1;
  }
  memset(tbs_cert->key_label, 0, sizeof(tbs_cert->key_label));
  memcpy(tbs_cert->key_label, cert_obj->name, key_label_size);
  tbs_cert->key_label_size = key_label_size;

  tbs_cert->hash_type = kHashTypeSha256;
  tbs_cert->curve_type = kCurveTypeP256;
  tbs_cert->signature_encoding = kSignatureEncodingDer;

  return 0;
}

// Helper function to extract a (fully formed) certificate from a perso blob.
int PackCertStruct(const perso_tlv_cert_obj_t* cert_obj, uint16_t cert_type,
                   endorse_cert_response_t* cert) {
  if (cert_obj == nullptr || cert == nullptr) {
    LOG(ERROR) << "Invalid cert_obj or cert object pointer.";
    return -1;
  }
  if (cert_type != kPersoObjectTypeX509Cert &&
      cert_type != kPersoObjectTypeCwtCert) {
    LOG(ERROR) << "Invalid cert type.";
    return -1;
  }

  // Set the cert type.
  if (cert_type == kPersoObjectTypeCwtCert) {
    cert->type = kCertTypeCwt;
  } else {
    cert->type = kCertTypeX509;
  }

  // Copy the certificate body.
  if (cert_obj->cert_body_size > kCertificateMaxSize) {
    LOG(ERROR) << "Certificate body size exceeds maximum: "
               << cert_obj->cert_body_size << " > " << kCertificateMaxSize;
    return -1;
  }
  memset(cert->cert, 0, sizeof(cert->cert));
  memcpy(cert->cert, cert_obj->cert_body_p, cert_obj->cert_body_size);
  cert->cert_size = cert_obj->cert_body_size;

  // Copy the key label.
  size_t key_label_size = strlen(cert_obj->name);
  if (key_label_size > kCertificateKeyLabelMaxSize) {
    LOG(ERROR) << "Key label size exceeds maximum: " << key_label_size << " > "
               << kCertificateKeyLabelMaxSize;
    return -1;
  }
  memset(cert->key_label, 0, sizeof(cert->key_label));
  memcpy(cert->key_label, cert_obj->name, key_label_size);
  cert->key_label_size = key_label_size;

  return 0;
}

// Helper function to extract a device ID from a perso blob.
int ExtractDeviceId(const uint8_t* buf, size_t buf_size,
                    device_id_bytes_t* device_id) {
  enum {
    kDeviceIdObjectSize =
        sizeof(device_id_bytes_t) + sizeof(perso_tlv_object_header_t)
  };

  if (buf_size < kDeviceIdObjectSize) {
    LOG(ERROR) << "Buffer too small for device ID object";
    return -1;
  }
  if (buf == nullptr || device_id == nullptr) {
    LOG(ERROR) << "Invalid input buffer or device ID pointer";
    return -1;
  }

  const perso_tlv_object_header_t* obj_hdr =
      reinterpret_cast<const perso_tlv_object_header_t*>(buf);
  uint16_t obj_size;
  uint16_t obj_type;

  PERSO_TLV_GET_FIELD(Objh, Size, *obj_hdr, &obj_size);
  PERSO_TLV_GET_FIELD(Objh, Type, *obj_hdr, &obj_type);

  if (obj_type == kPersoObjectTypeDeviceId) {
    if (obj_size !=
        sizeof(device_id_bytes_t) + sizeof(perso_tlv_object_header_t)) {
      LOG(ERROR) << "Invalid device ID object size: " << obj_size
                 << ", expected: "
                 << (sizeof(device_id_bytes_t) +
                     sizeof(perso_tlv_object_header_t));
      return -1;
    }
    memcpy(device_id->raw, buf + sizeof(perso_tlv_object_header_t),
           sizeof(device_id_bytes_t));
    return 0;
  }
  LOG(ERROR) << "Invalid object type for device ID: " << obj_type
             << ", expected: " << kPersoObjectTypeDeviceId;
  return -1;
}

// Helper function to pack a certificate object into a perso blob.
int PackCertTlvObject(const endorse_cert_response_t* cert, perso_blob_t* blob) {
  // Calculate the size of the object header and certificate header.
  size_t cert_entry_size =
      sizeof(perso_tlv_cert_header_t) + cert->key_label_size + cert->cert_size;
  size_t obj_size = sizeof(perso_tlv_object_header_t) + cert_entry_size;

  if (blob->next_free + obj_size > sizeof(blob->body)) {
    LOG(ERROR) << "Personalization blob is full, cannot add more objects.";
    return -1;
  }

  // Set up the object header.
  uint8_t* buf = blob->body + blob->next_free;
  perso_tlv_object_header_t* obj_hdr =
      reinterpret_cast<perso_tlv_object_header_t*>(buf);
  PERSO_TLV_SET_FIELD(Objh, Size, *obj_hdr, obj_size);
  if (cert->type == kCertTypeCwt) {
    PERSO_TLV_SET_FIELD(Objh, Type, *obj_hdr, kPersoObjectTypeCwtCert);
  } else {
    PERSO_TLV_SET_FIELD(Objh, Type, *obj_hdr, kPersoObjectTypeX509Cert);
  }

  // Set up the certificate header.
  buf += sizeof(perso_tlv_object_header_t);
  perso_tlv_cert_header_t* cert_hdr =
      reinterpret_cast<perso_tlv_cert_header_t*>(buf);
  PERSO_TLV_SET_FIELD(Crth, Size, *cert_hdr, cert_entry_size);
  PERSO_TLV_SET_FIELD(Crth, NameSize, *cert_hdr, cert->key_label_size);

  // Copy the certificate name string.
  buf += sizeof(perso_tlv_cert_header_t);
  memcpy(buf, cert->key_label, cert->key_label_size);

  // Copy the certificate data.
  buf += cert->key_label_size;
  memcpy(buf, cert->cert, cert->cert_size);

  // Update the next free offset in the blob.
  blob->next_free += obj_size;
  blob->num_objects++;

  return 0;
}

// Helper function to pack a seed object into a perso blob.
int PackSeedTlvObject(const seed_t* seed, perso_blob_t* blob) {
  // Calculate the size of the object .
  size_t obj_size = sizeof(perso_tlv_object_header_t) + seed->size;
  if (blob->next_free + obj_size > sizeof(blob->body)) {
    LOG(ERROR) << "Personalization blob is full, cannot add more objects.";
    return -1;
  }

  // Set up the object header.
  uint8_t* buf = blob->body + blob->next_free;
  perso_tlv_object_header_t* obj_hdr =
      reinterpret_cast<perso_tlv_object_header_t*>(buf);
  PERSO_TLV_SET_FIELD(Objh, Size, *obj_hdr, obj_size);
  PERSO_TLV_SET_FIELD(Objh, Type, *obj_hdr, seed->type);

  // Copy the certificate data.
  buf += sizeof(perso_tlv_object_header_t);
  memcpy(buf, seed->raw, seed->size);

  // Update the next free offset in the blob.
  blob->next_free += obj_size;
  blob->num_objects++;

  return 0;
}

}  // namespace

DLLEXPORT int UnpackPersoBlob(
    const perso_blob_t* blob, device_id_bytes_t* device_id,
    endorse_cert_signature_t* signature, sha256_hash_t* perso_fw_hash,
    endorse_cert_request_t* tbs_certs, size_t* tbs_cert_count,
    endorse_cert_response_t* certs, size_t* cert_count, seed_t* seeds,
    size_t* seed_count) {
  if (device_id == nullptr || signature == nullptr ||
      perso_fw_hash == nullptr || tbs_certs == nullptr ||
      tbs_cert_count == nullptr || certs == nullptr || cert_count == nullptr ||
      seeds == nullptr || seed_count == nullptr) {
    LOG(ERROR) << "Invalid output parameters";
    return -1;
  }

  if (blob == nullptr || blob->next_free == 0) {
    LOG(ERROR) << "Invalid personalization blob";
    return -1;  // Invalid blob
  }

  memset(device_id->raw, 0, sizeof(device_id_bytes_t));
  memset(signature->raw, 0, sizeof(endorse_cert_signature_t));
  memset(perso_fw_hash->raw, 0, sizeof(sha256_hash_t));

  size_t max_tbs_cert_count = *tbs_cert_count;
  *tbs_cert_count = 0;
  size_t max_cert_count = *cert_count;
  *cert_count = 0;
  size_t max_seed_count = *seed_count;
  *seed_count = 0;

  const uint8_t* buf = blob->body;
  size_t remaining = blob->next_free;

  if (remaining > sizeof(blob->body)) {
    LOG(ERROR) << "Remaining buffer size exceeds maximum allowed: " << remaining
               << " > " << sizeof(blob->body);
    return -1;
  }

  while (remaining >= sizeof(perso_tlv_object_header_t)) {
    const perso_tlv_object_header_t* obj_hdr =
        reinterpret_cast<const perso_tlv_object_header_t*>(buf);
    uint16_t obj_size;
    uint16_t obj_type;

    PERSO_TLV_GET_FIELD(Objh, Size, *obj_hdr, &obj_size);
    PERSO_TLV_GET_FIELD(Objh, Type, *obj_hdr, &obj_type);

    if (obj_size > remaining) {
      LOG(ERROR) << "Object size exceeds remaining buffer size: " << obj_size
                 << " > " << remaining;
      return -1;
    }

    switch (obj_type) {
      case kPersoObjectTypeDeviceId: {
        if (ExtractDeviceId(buf, obj_size, device_id) != 0) {
          LOG(ERROR) << "Failed to extract device ID";
          return -1;
        }
        break;
      }

      case kPersoObjectTypeX509Tbs: {
        if (*tbs_cert_count >= max_tbs_cert_count) {
          LOG(ERROR) << "Exceeded maximum number of TBS certificates: "
                     << *tbs_cert_count << " >= " << max_tbs_cert_count;
          return -1;
        }
        perso_tlv_cert_obj_t cert_obj;
        if (ExtractCertObject(buf, obj_size, &cert_obj) != 0) {
          LOG(ERROR) << "Failed to extract X509 TBS certificate object";
          return -1;
        }
        if (PackX509TbsCertStruct(&cert_obj, &tbs_certs[*tbs_cert_count]) !=
            0) {
          LOG(ERROR) << "Failed to pack TBS certificate endorsement request.";
          return -1;
        }
        (*tbs_cert_count)++;
        break;
      }

      case kPersoObjectTypeX509Cert:
      case kPersoObjectTypeCwtCert: {
        if (*cert_count >= max_cert_count) {
          LOG(ERROR) << "Exceeded maximum number of certificates: "
                     << *cert_count << " >= " << max_cert_count;
          return -1;
        }
        perso_tlv_cert_obj_t cert_obj;
        if (ExtractCertObject(buf, obj_size, &cert_obj) != 0) {
          LOG(ERROR) << "Failed to extract X509 certificate object";
          return -1;
        }
        if (PackCertStruct(&cert_obj, obj_type, &certs[*cert_count]) != 0) {
          LOG(ERROR) << "Failed to pack TBS certificate endorsement request.";
          return -1;
        }
        (*cert_count)++;
        break;
      }

      case kPersoObjectTypeWasTbsHmac: {
        if (obj_size != sizeof(endorse_cert_signature_t) +
                            sizeof(perso_tlv_object_header_t)) {
          LOG(ERROR) << "Invalid size for WAS TBS HMAC object: " << obj_size
                     << ", expected: "
                     << (sizeof(endorse_cert_signature_t) +
                         sizeof(perso_tlv_object_header_t));
          return -1;
        }
        memcpy(signature->raw, buf + sizeof(perso_tlv_object_header_t),
               sizeof(signature->raw));
        break;
      }

      case kPersoObjectTypeDevSeed:
      case kPersoObjectTypeGenericSeed: {
        if (*seed_count >= max_seed_count) {
          LOG(ERROR) << "Exceeded maximum number of seeds: " << *seed_count
                     << " >= " << max_seed_count;
          return -1;
        }
        // The size of a "dev_seed" is the maximum size a seed can be.
        if (obj_size > kDevSeedBytesSize + sizeof(perso_tlv_object_header_t)) {
          LOG(ERROR) << "Invalid seed object size: " << obj_size
                     << ", expected size <=: "
                     << (kDevSeedBytesSize + sizeof(perso_tlv_object_header_t));
          return -1;
        }
        seeds[*seed_count].size = obj_size - sizeof(perso_tlv_object_header_t);
        seeds[*seed_count].type = obj_type;
        memcpy(seeds[*seed_count].raw, buf + sizeof(perso_tlv_object_header_t),
               seeds[*seed_count].size);
        (*seed_count)++;
        break;
      }

      case kPersoObjectTypePersoSha256Hash: {
        if (obj_size !=
            sizeof(sha256_hash_t) + sizeof(perso_tlv_object_header_t)) {
          LOG(ERROR) << "Invalid size for perso firmware hash object: "
                     << obj_size << ", expected: "
                     << (sizeof(sha256_hash_t) +
                         sizeof(perso_tlv_object_header_t));
          return -1;
        }
        memcpy(perso_fw_hash->raw, buf + sizeof(perso_tlv_object_header_t),
               sizeof(perso_fw_hash->raw));
        break;
      }
    }

    buf += obj_size;
    remaining -= obj_size;
  }

  bool all_zero = true;
  for (size_t i = 0; i < sizeof(signature->raw); ++i) {
    if (signature->raw[i] != 0) {
      all_zero = false;
      break;
    }
  }
  if (all_zero) {
    LOG(ERROR) << "No WAS TBS HMAC found in the blob";
    return -1;
  }

  if (*tbs_cert_count == 0) {
    LOG(ERROR) << "No TBS certificates found in the blob";
    return -1;
  }
  uint32_t device_id_sum = 0;
  for (size_t i = 0; i < sizeof(device_id_bytes_t); i++) {
    device_id_sum += device_id->raw[i];
  }
  if (device_id_sum == 0) {
    LOG(ERROR) << "Device ID is empty";
    return -1;
  }

  return 0;
}

DLLEXPORT int PackPersoBlob(size_t cert_count,
                            const endorse_cert_response_t* certs,
                            size_t ca_cert_count,
                            const endorse_cert_response_t* ca_certs,
                            perso_blob_t* blob) {
  if (blob == nullptr) {
    LOG(ERROR) << "Invalid personalization blob pointer";
    return -1;
  }
  if (cert_count == 0 || certs == nullptr) {
    LOG(ERROR) << "Invalid certificate count or certs pointer";
    return -1;
  }

  memset(blob, 0, sizeof(perso_blob_t));

  for (size_t i = 0; i < cert_count; i++) {
    const endorse_cert_response_t& cert = certs[i];
    if (cert.cert_size == 0) {
      LOG(ERROR) << "Invalid certificate at index " << i;
      return -1;
    }
    if (PackCertTlvObject(&cert, blob) != 0) {
      LOG(ERROR) << "Unable to pack certificate into perso blob.";
      return -1;
    }
  }

  for (size_t i = 0; i < ca_cert_count; i++) {
    const endorse_cert_response_t& cert = ca_certs[i];
    if (cert.cert_size == 0) {
      LOG(ERROR) << "Invalid CA certificate at index " << i;
      return -1;
    }
    if (PackCertTlvObject(&cert, blob) != 0) {
      LOG(ERROR) << "Unable to pack CA certificate into perso blob.";
      return -1;
    }
  }

  return 0;
}

DLLEXPORT int PackRegistryPersoTlvData(
    const endorse_cert_response_t* certs_endorsed_by_dut,
    size_t num_certs_endorsed_by_dut,
    const endorse_cert_response_t* certs_endorsed_by_spm,
    size_t num_certs_endorsed_by_spm, const seed_t* seeds, size_t num_seeds,
    perso_blob_t* output) {
  if (certs_endorsed_by_dut == nullptr || certs_endorsed_by_spm == nullptr ||
      output == nullptr) {
    LOG(ERROR) << "Invalid certs or personalization blob pointer.";
    return -1;
  }
  if (num_certs_endorsed_by_dut == 0 && num_certs_endorsed_by_spm == 0 &&
      num_seeds == 0) {
    LOG(ERROR) << "No certs or seeds to send to registry.";
    return -1;
  }

  memset(output, 0, sizeof(perso_blob_t));

  // Pack all cert objects.
  const endorse_cert_response_t* all_certs[] = {certs_endorsed_by_dut,
                                                certs_endorsed_by_spm};
  size_t num_certs[] = {num_certs_endorsed_by_dut, num_certs_endorsed_by_spm};
  for (size_t i = 0; i < (sizeof(all_certs) / sizeof(all_certs[0])); i++) {
    for (size_t j = 0; j < num_certs[i]; j++) {
      const endorse_cert_response_t& cert = all_certs[i][j];
      if (cert.cert_size == 0) {
        LOG(ERROR) << "Invalid certificate at indices i:" << i << ", j:" << j;
        return -1;
      }
      if (PackCertTlvObject(&cert, output) != 0) {
        LOG(ERROR) << "Unable to pack certificate into perso blob.";
        return -1;
      }
    }
  }

  // Pack all seed objects.
  for (size_t i = 0; i < num_seeds; i++) {
    const seed_t& seed = seeds[i];
    if (seed.size == 0) {
      LOG(ERROR) << "Invalid seed at index " << i;
      return -1;
    }
    if (PackSeedTlvObject(&seed, output) != 0) {
      LOG(ERROR) << "Unable to pack seed into perso blob.";
      return -1;
    }
  }

  return 0;
}
