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
	"math/rand"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	kwp "github.com/google/tink/go/kwp/subtle"
	"golang.org/x/crypto/hkdf"

	"github.com/lowRISC/opentitan-provisioning/src/cert/signer"
	"github.com/lowRISC/opentitan-provisioning/src/cert/templates/tpm"
	"github.com/lowRISC/opentitan-provisioning/src/pk11"
	ts "github.com/lowRISC/opentitan-provisioning/src/pk11/test_support"
	certloader "github.com/lowRISC/opentitan-provisioning/src/spm/services/certloader"
)

// Creates a new HSM for a test by reaching into the tests's SoftHSM token.
//
// Returns the hsm, the (host-side) bytes of the KG, and the (host-side) bytes
// of the KT.
func MakeHSM(t *testing.T) (*HSM, []byte, []byte) {
	t.Helper()
	s := ts.GetSession(t)
	ts.Check(t, s.Login(pk11.NormalUser, ts.UserPin))

	global, err := s.GenerateAES(256, &pk11.KeyOptions{Extractable: true})
	ts.Check(t, err)
	gUID, err := global.UID()
	ts.Check(t, err)
	globalBytes, err := global.ExportKey()
	ts.Check(t, err)

	secret := []byte("this is secret data for generating keys from")
	transport, err := s.ImportKeyMaterial(secret, &pk11.KeyOptions{Extractable: true})
	ts.Check(t, err)
	tUID, err := transport.UID()
	ts.Check(t, err)

	numSessions := 1
	sessions := newSessionQueue(numSessions)
	err = sessions.insert(s)
	ts.Check(t, err)

	return &HSM{KG: gUID, KT: tUID, sessions: sessions}, []byte(globalBytes.(pk11.AESKey)), secret
}

func TestTransport(t *testing.T) {
	hsm, kg, kt := MakeHSM(t)

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
	hsm, kg, _ := MakeHSM(t)

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
	if err != nil {
		t.Fatalf("unable to build subject alteraive name, error: %v", err)
	}

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

	hsm.Kca = caKeyHandle
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
