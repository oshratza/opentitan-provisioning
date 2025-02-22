// Copyright lowRISC contributors (OpenTitan project).
// Licensed under the Apache License, Version 2.0, see LICENSE for details.
// SPDX-License-Identifier: Apache-2.0

package se

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"io"
	"log"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/bazelbuild/rules_go/go/tools/bazel"
	"github.com/google/go-cmp/cmp"
	kwp "github.com/google/tink/go/kwp/subtle"
	"golang.org/x/crypto/hkdf"
	"golang.org/x/crypto/sha3"

	"github.com/lowRISC/opentitan-provisioning/src/cert/signer"
	"github.com/lowRISC/opentitan-provisioning/src/cert/templates/tpm"
	"github.com/lowRISC/opentitan-provisioning/src/pk11"
	ts "github.com/lowRISC/opentitan-provisioning/src/pk11/test_support"
	certloader "github.com/lowRISC/opentitan-provisioning/src/spm/services/certloader"
)

const (
	diceTBSPath    = "src/spm/services/testdata/tbs.der"
	diceCAKeyPath  = "src/spm/services/testdata/sk.pkcs8.der"
	diceCACertPath = "src/spm/services/testdata/dice_ca.pem"
)

// readFile reads a file from the runfiles directory.
func readFile(t *testing.T, filename string) []byte {
	t.Helper()
	filename, err := bazel.Runfile(filename)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("unable to load file: %q, error: %v", filename, err)
	}
	return data
}

// Creates a new HSM for a test by reaching into the tests's SoftHSM token.
//
// Returns the hsm, the (host-side) bytes of the KG, and the (host-side) bytes
// of the KT.
func MakeHSM(t *testing.T) (*HSM, []byte, []byte, []byte, []byte) {
	t.Helper()
	s := ts.GetSession(t)
	ts.Check(t, s.Login(pk11.NormalUser, ts.UserPin))

	// Initialize HSM with KG.
	global, err := s.GenerateAES(256, &pk11.KeyOptions{Extractable: true})
	ts.Check(t, err)
	gUID, err := global.UID()
	ts.Check(t, err)
	globalKeyBytes, err := global.ExportKey()
	ts.Check(t, err)

	// Initialize HSM with KT.
	transportKeySeed := []byte("this is secret data for generating keys from")
	transport, err := s.ImportKeyMaterial(transportKeySeed, &pk11.KeyOptions{Extractable: true})
	ts.Check(t, err)
	tUID, err := transport.UID()
	ts.Check(t, err)

	// Initialize HSM with KHsks.
	hsKeySeed := []byte("high security KDF seed")
	hsks, err := s.ImportKeyMaterial(hsKeySeed, &pk11.KeyOptions{Extractable: false})
	ts.Check(t, err)
	hsksUID, err := hsks.UID()
	ts.Check(t, err)

	// Initialize HSM with KLsks.
	lsKeySeed := []byte("low security KDF seed")
	lsks, err := s.ImportKeyMaterial(lsKeySeed, &pk11.KeyOptions{Extractable: false})
	ts.Check(t, err)
	lsksUID, err := lsks.UID()
	ts.Check(t, err)

	// Initialize session queue.
	numSessions := 1
	sessions := newSessionQueue(numSessions)
	err = sessions.insert(s)
	ts.Check(t, err)

	return &HSM{
			SymmetricKeys: map[string][]byte{
				"KG":             gUID,
				"KT":             tUID,
				"HighSecKdfSeed": hsksUID,
				"LowSecKdfSeed":  lsksUID,
			},
			PrivateKeys: map[string][]byte{},
			sessions:    sessions,
		},
		[]byte(globalKeyBytes.(pk11.AESKey)),
		transportKeySeed,
		hsKeySeed,
		lsKeySeed
}

func TestGenerateSymmKeys(t *testing.T) {
	hsm, _, _, hsKeySeed, lsKeySeed := MakeHSM(t)

	// Symmetric keygen parameters.
	// test unlock token
	testUnlockTokenParams := SymmetricKeygenParams{
		UseHighSecuritySeed: false,
		KeyType:             SymmetricKeyTypeRaw,
		SizeInBits:          128,
		Sku:                 "test sku",
		Diversifier:         "test_unlock",
	}
	// test exit token
	testExitTokenParams := SymmetricKeygenParams{
		UseHighSecuritySeed: false,
		KeyType:             SymmetricKeyTypeRaw,
		SizeInBits:          128,
		Sku:                 "test sku",
		Diversifier:         "test_exit",
	}
	// RMA token
	rmaTokenParams := SymmetricKeygenParams{
		UseHighSecuritySeed: true,
		KeyType:             SymmetricKeyTypeHashedOtLcToken,
		SizeInBits:          128,
		Sku:                 "test sku",
		Diversifier:         "rma: device_id",
	}
	// wafer authentication secret
	wasParams := SymmetricKeygenParams{
		UseHighSecuritySeed: true,
		KeyType:             SymmetricKeyTypeRaw,
		SizeInBits:          256,
		Sku:                 "test sku",
		Diversifier:         "was",
	}
	params := []*SymmetricKeygenParams{
		&testUnlockTokenParams,
		&testExitTokenParams,
		&rmaTokenParams,
		&wasParams,
	}

	// Generate the actual keys (using the HSM).
	keys, err := hsm.GenerateSymmetricKeys(params)
	ts.Check(t, err)

	// Check actual keys match those generated using the go crypto package.
	for i, p := range params {
		// Generate expected key.
		var keyGenerator io.Reader
		if p.UseHighSecuritySeed {
			keyGenerator = hkdf.New(crypto.SHA256.New, hsKeySeed, []byte(p.Sku), []byte(p.Diversifier))
		} else {
			keyGenerator = hkdf.New(crypto.SHA256.New, lsKeySeed, []byte(p.Sku), []byte(p.Diversifier))
		}
		expected_key := make([]byte, len(keys[i]))
		keyGenerator.Read(expected_key)
		if p.KeyType == SymmetricKeyTypeHashedOtLcToken {
			hasher := sha3.NewCShake128([]byte(""), []byte("LC_CTRL"))
			hasher.Write(expected_key)
			hasher.Read(expected_key)
		}

		// Check the actual and expected keys are equal.
		log.Printf("Actual   Key: %q", hex.EncodeToString(keys[i]))
		log.Printf("Expected Key: %q", hex.EncodeToString(expected_key))
		if !bytes.Equal(keys[i], expected_key) {
			t.Fatal("symmetric keygen failed")
		}
	}
}

func TestTransport(t *testing.T) {
	log.Printf("TestTransport")
	hsm, kg, kt, _, _ := MakeHSM(t)

	key, err := hsm.DeriveAndWrapTransportSecret([]byte("my device id"))
	ts.Check(t, err)

	kwp, err := kwp.NewKWP(kg)
	ts.Check(t, err)
	unwrap, err := kwp.Unwrap(key)
	ts.Check(t, err)

	hkdf := hkdf.New(crypto.SHA256.New, kt, []byte("my device id"), transportKeyLabel)
	expected := make([]byte, len(unwrap))
	hkdf.Read(expected)

	if !bytes.Equal(unwrap, expected) {
		t.Fatal("decryption failure")
	}
}

// CreateCAKeys generates a Certificate Authority (CA) key pair which can be
// used in any test case. It requires an initialized `hsm` instance.
func CreateCAKeys(t *testing.T, hsm *HSM) (pk11.KeyPair, error) {
	session, release := hsm.sessions.getHandle()
	defer release()
	return session.GenerateECDSA(elliptic.P256(), &pk11.KeyOptions{Extractable: true})
}

func TestGenerateCert(t *testing.T) {
	log.Printf("TestGenerateCert")
	hsm, kg, _, _, _ := MakeHSM(t)

	ca, err := CreateCAKeys(t, hsm)
	ts.Check(t, err)
	caKeyHandle, err := ca.PrivateKey.UID()
	ts.Check(t, err)

	caPrivHostI, err := ca.PrivateKey.ExportKey()
	ts.Check(t, err)
	caPrivHost := caPrivHostI.(*ecdsa.PrivateKey)
	caPubHostI, err := ca.PublicKey.ExportKey()
	ts.Check(t, err)
	caPubHost := caPubHostI.(*ecdsa.PublicKey)

	epoch, err := time.Parse(time.RFC3339, "2022-01-01T00:00:00.000Z")
	ts.Check(t, err)

	b := tpm.New()

	subjectAltName, err := certloader.BuildSubjectAltName(
		certloader.CertificateSubjectAltName{
			Manufacturer: "id:4E544300",
			Model:        "NPCT75x",
			Version:      "id:00070002",
		})
	ts.Check(t, err)

	template, err := b.Build(&signer.Params{
		SerialNumber: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19},
		Issuer: pkix.Name{
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"OpenTitan"},
			OrganizationalUnit: []string{"Engineering"},
		},
		Subject: pkix.Name{
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"OpenTitan"},
			OrganizationalUnit: []string{"Engineering"},
		},
		NotBefore:             epoch,
		NotAfter:              epoch.Add(time.Hour * 24 * 31),
		KeyUsage:              x509.KeyUsageCertSign,
		IsCA:                  true,
		BasicConstraintsValid: true,
		IssuingCertificateURL: nil,
		SubjectAltName:        subjectAltName,
	})

	ts.Check(t, err)
	caCertBytes, err := signer.CreateCertificate(template, template, caPubHost, caPrivHost)
	caCert, err := x509.ParseCertificate(caCertBytes)
	ts.Check(t, err)

	// TPM extensions marked as critical end up in this list. We explicitly
	// clear the list to get x509.Verify to pass.
	caCert.UnhandledCriticalExtensions = nil

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	template, err = b.Build(&signer.Params{
		SerialNumber: []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19},
		Issuer:       template.Subject,
		Subject: pkix.Name{
			Country:            []string{"US"},
			Province:           []string{"California"},
			Organization:       []string{"OpenTitan"},
			OrganizationalUnit: []string{"Engineering"},
			CommonName:         "OpenTitan TPM EK",
		},
		NotBefore:             epoch,
		NotAfter:              epoch.Add(time.Hour * 24 * 31),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		IsCA:                  false,
		BasicConstraintsValid: true,
		IssuingCertificateURL: nil,
		SubjectAltName:        subjectAltName,
	})
	ts.Check(t, err)

	hsm.PrivateKeys["KCAPriv"] = caKeyHandle
	certs, err := hsm.GenerateKeyPairAndCert(caCert, []SigningParams{{template, elliptic.P256()}})
	ts.Check(t, err)

	cert, err := x509.ParseCertificate(certs[0].Cert)
	ts.Check(t, err)

	// TPM extensions marked as critical end up in this list. We explicitly
	// clear the list to get x509.Verify to pass.
	cert.UnhandledCriticalExtensions = nil

	_, err = cert.Verify(x509.VerifyOptions{
		Roots:       roots,
		CurrentTime: epoch.Add(time.Hour * 200),
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsage(x509.KeyUsageDigitalSignature)},
	})
	ts.Check(t, err)

	kwp, err := kwp.NewKWP(kg)
	ts.Check(t, err)
	unwrap, err := kwp.Unwrap(certs[0].WrappedKey)
	ts.Check(t, err)

	privI, err := x509.ParsePKCS8PrivateKey(unwrap)
	ts.Check(t, err)
	pub := cert.PublicKey.(*ecdsa.PublicKey)
	priv := privI.(*ecdsa.PrivateKey)

	if diff := cmp.Diff(pub, &priv.PublicKey); diff != "" {
		t.Errorf("unexpected diff (-want +got):\n%s", diff)
	}

	bytes := []byte("a message to sign")
	r, s, err := ecdsa.Sign(rand.New(rand.NewSource(0)), priv, bytes)
	ts.Check(t, err)
	if !ecdsa.Verify(pub, bytes, r, s) {
		t.Fatal("verification failed")
	}
}

func TestEndorseCert(t *testing.T) {
	log.Printf("TestEndorseCert")
	hsm, _, _, _, _ := MakeHSM(t)

	const kcaPrivName = "kca_priv"

	// The following nested function is required to avoid deadlocks when calling
	// `hsm.sessions.getHandle()` in the `EndorseCert` function.
	importCAKey := func() {
		privateKeyDer := readFile(t, diceCAKeyPath)
		privateKey, err := x509.ParsePKCS8PrivateKey(privateKeyDer)
		ts.Check(t, err)

		// Import the CA key into the HSM.
		session, release := hsm.sessions.getHandle()
		defer release()

		// Cast the private key to an ECDSA private key to make sure the
		// `ImportKey` function imports the key as an ECDSA key.
		privateKey = privateKey.(*ecdsa.PrivateKey)
		ca, err := session.ImportKey(privateKey, &pk11.KeyOptions{
			Extractable: true,
			Token:       true,
		})
		ts.Check(t, err)
		err = ca.SetLabel(kcaPrivName)
		ts.Check(t, err)
	}

	importCAKey()
	caCertPEM := readFile(t, diceCACertPath)
	caCertBlock, _ := pem.Decode(caCertPEM)
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	ts.Check(t, err)

	roots := x509.NewCertPool()
	roots.AddCert(caCert)

	log.Printf("Reading TBS")
	tbs := readFile(t, diceTBSPath)

	log.Printf("Endorsing cert")
	certDER, err := hsm.EndorseCert(tbs, EndorseCertParams{
		KeyLabel:           kcaPrivName,
		SignatureAlgorithm: x509.ECDSAWithSHA256,
	})
	ts.Check(t, err)

	cert, err := x509.ParseCertificate(certDER)
	ts.Check(t, err)

	// DICE extensions marked as critical end up in this list. We explicitly
	// clear the list to get x509.Verify to pass.
	cert.UnhandledCriticalExtensions = nil

	_, err = cert.Verify(x509.VerifyOptions{
		Roots: roots,
	})
	ts.Check(t, err)
}
