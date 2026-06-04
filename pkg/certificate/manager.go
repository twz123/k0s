// SPDX-FileCopyrightText: 2020 k0s authors
// SPDX-License-Identifier: Apache-2.0

package certificate

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	internalos "github.com/k0sproject/k0s/internal/os"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/internal/pkg/stringslice"
	"github.com/k0sproject/k0s/pkg/constant"

	"github.com/cloudflare/cfssl/certinfo"
	"github.com/cloudflare/cfssl/cli/genkey"
	"github.com/cloudflare/cfssl/config"
	"github.com/cloudflare/cfssl/csr"
	"github.com/cloudflare/cfssl/helpers"
	"github.com/cloudflare/cfssl/initca"
	"github.com/cloudflare/cfssl/signer"
	"github.com/cloudflare/cfssl/signer/local"
	"github.com/sirupsen/logrus"
)

// Request defines the certificate request fields
type Request struct {
	Name      string
	CN        string
	O         string
	CA        Ref
	Hostnames []string
}

type Ref interface {
	resolve() (*Data, error)
}

// Data is a helper struct to be able to return the created key and cert data
type Data struct {
	Key  []byte
	Cert []byte
}

func (c *Data) resolve() (*Data, error) {
	return new(*c), nil
}

type Files struct {
	CertPath, KeyPath string
}

func (f *Files) Load() (*Data, error) {
	cert, err := os.ReadFile(f.CertPath)
	if err != nil {
		return nil, err
	}
	key, err := os.ReadFile(f.KeyPath)
	if err != nil {
		return nil, err
	}
	return &Data{Key: key, Cert: cert}, nil
}

func (f *Files) Save(data *Data, ownerID int) (err error) {
	// FIXME needs docs and comments

	keyFile, err := file.AtomicWithTarget(f.KeyPath).
		TryWithOwnerAndGroup(ownerID, -1).
		WithPermissions(constant.CertSecureMode).
		Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, keyFile.Close()) }()

	certFile, err := file.AtomicWithTarget(f.CertPath).
		TryWithOwnerAndGroup(ownerID, -1).
		WithPermissions(constant.CertMode).
		Open()
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, certFile.Close()) }()

	if _, err := keyFile.Write(data.Key); err != nil {
		return err
	}
	if _, err := certFile.Write(data.Cert); err != nil {
		return err
	}

	var restoreBackup func() error
	for certDir, attempt := filepath.Dir(f.CertPath), 1; ; attempt++ {
		backupPath := filepath.Join(certDir, "."+rand.Text()+".tmp")
		if err := internalos.RenameNoReplace(f.CertPath, backupPath); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				restoreBackup = func() error { return nil }
				break
			}

			if errors.Is(err, errors.ErrUnsupported) {
				if _, err := os.Stat(f.CertPath); err != nil {
					if errors.Is(err, os.ErrNotExist) {
						restoreBackup = func() error { return nil }
						break
					}
					return err
				}
				restoreBackup = func() error {
					return fmt.Errorf("can't restore old certificate (%w)", err)
				}
				break
			}

			if attempt <= 100 && errors.Is(err, os.ErrExist) {
				continue
			}

			return err
		}
		defer func() {
			if rmErr := os.Remove(backupPath); err != nil && !errors.Is(err, os.ErrNotExist) {
				err = errors.Join(err, rmErr)
			}
		}()
		restoreBackup = func() error {
			if err := os.Rename(backupPath, f.CertPath); err != nil {
				return fmt.Errorf("failed to restore certificate from backup: %w", err)
			}
			return nil
		}
		break
	}

	if err := certFile.Finish(); err != nil {
		return err
	}
	if err := keyFile.Finish(); err != nil {
		return errors.Join(err, restoreBackup())
	}

	return nil
}

func (f *Files) resolve() (*Data, error) {
	return f.Load()
}

type StoredCertificate struct {
	Data
	Files
}

// Manager is the certificate manager
type Manager struct {
	rootDir string
}

func NewManager(rootDir string) *Manager {
	return &Manager{rootDir}
}

func (m *Manager) Named(name string) *Files {
	return &Files{
		CertPath: filepath.Join(m.rootDir, name+".crt"),
		KeyPath:  filepath.Join(m.rootDir, name+".key"),
	}
}

// EnsureCA makes sure the given CA certs and key is created.
func (m *Manager) EnsureCA(name, cn string, expiry time.Duration) (Ref, error) {
	keyFile := filepath.Join(m.rootDir, name+".key")
	certFile := filepath.Join(m.rootDir, name+".crt")

	if cert, err := m.Named(name).resolve(); err == nil {
		return cert, nil
	}

	req := new(csr.CertificateRequest)
	req.KeyRequest = csr.NewKeyRequest()
	req.KeyRequest.A = "rsa"
	req.KeyRequest.S = 2048
	req.CN = cn
	req.CA = &csr.CAConfig{
		Expiry: expiry.String(),
	}
	cert, _, key, err := initca.New(req)
	if err != nil {
		return nil, err
	}

	err = file.WriteContentAtomically(keyFile, key, constant.CertSecureMode)
	if err != nil {
		return nil, err
	}

	err = file.WriteContentAtomically(certFile, cert, constant.CertMode)
	if err != nil {
		return nil, err
	}

	return &Data{Key: key, Cert: cert}, nil
}

// EnsureCertificate creates the specified certificate if it does not already exist
func (m *Manager) EnsureCertificate(certReq Request, ownerID int, expiry time.Duration) (Data, error) {

	certFiles := m.Named(certReq.Name)

	// if regenerateCert returns true, it means we need to create the certs
	regenerateCert, err := m.regenerateCert(certFiles)
	if err != nil {
		return Data{}, err
	}
	if regenerateCert {
		logrus.Debugf("creating certificate %s", certFiles.CertPath)
		req := csr.CertificateRequest{
			KeyRequest: csr.NewKeyRequest(),
			CN:         certReq.CN,
			Names: []csr.Name{
				{O: certReq.O},
			},
		}

		req.KeyRequest.A = "rsa"
		req.KeyRequest.S = 2048
		req.Hosts = stringslice.Unique(certReq.Hostnames)

		g := &csr.Generator{Validator: genkey.Validator}
		csrBytes, key, err := g.ProcessRequest(&req)
		if err != nil {
			return Data{}, err
		}

		if certReq.CA == nil {
			return Data{}, errors.New("no CA reference provided")
		}
		caCert, err := certReq.CA.resolve()
		if err != nil {
			return Data{}, fmt.Errorf("failed to resolve CA certificate: %w", err)
		}

		cert, err := helpers.ParseCertificatePEM(caCert.Cert)
		if err != nil {
			return Data{}, err
		}

		var password []byte
		if pw := os.Getenv("CFSSL_CA_PK_PASSWORD"); pw != "" {
			password = []byte(pw)
		}
		caKey, err := helpers.ParsePrivateKeyPEMWithPassword(caCert.Key, password)
		if err != nil {
			return Data{}, fmt.Errorf("malformed private key %w", err)
		}

		s, err := local.NewSigner(caKey, cert, signer.DefaultSigAlgo(caKey), &config.Signing{
			Profiles: map[string]*config.SigningProfile{},
			Default: &config.SigningProfile{
				Usage:        []string{"signing", "key encipherment", "server auth", "client auth"},
				Expiry:       expiry,
				ExpiryString: expiry.String(),
			},
		})
		if err != nil {
			return Data{}, err
		}

		signReq := signer.SignRequest{
			Request: string(csrBytes),
			Profile: "kubernetes",
		}

		signedCertData, err := s.Sign(signReq)
		if err != nil {
			return Data{}, err
		}
		c := Data{
			Key:  key,
			Cert: signedCertData,
		}

		if err := certFiles.Save(&c, ownerID); err != nil {
			return Data{}, err
		}

		return c, nil
	}

	// certs exist, let's just verify their permissions
	_ = os.Chown(certFiles.KeyPath, ownerID, -1)
	_ = os.Chown(certFiles.CertPath, ownerID, -1)

	if resolved, err := certFiles.resolve(); err != nil {
		return Data{}, err
	} else {
		return *resolved, nil
	}
}

// if regenerateCert does not need to do any changes, it will return false
// if a change in SAN hosts is detected, if will return true, to re-generate certs
func (m *Manager) regenerateCert(certFiles *Files) (bool, error) {
	var cert *certinfo.Certificate
	var err error

	// if certificate & key don't exist, return true, in order to generate certificates
	if !file.Exists(certFiles.KeyPath) && !file.Exists(certFiles.CertPath) {
		return true, nil
	}

	if cert, err = certinfo.ParseCertificateFile(certFiles.CertPath); err != nil {
		logrus.Warnf("unable to parse certificate file at %s: %v", certFiles.CertPath, err)
		return true, nil
	}

	if managed, err := m.isManagedByK0s(cert); err != nil || managed {
		return managed, err
	}

	logrus.Debugf("cert regeneration not needed for %s, not managed by k0s: %s", certFiles.CertPath, cert.Issuer.CommonName)
	return false, nil
}

// checks if the cert issuer (CA) is a k0s setup one
func (m *Manager) isManagedByK0s(cert *certinfo.Certificate) (bool, error) {
	ca, err := certinfo.ParseCertificateFile(filepath.Join(m.rootDir, "ca.crt"))
	if err != nil {
		return false, fmt.Errorf("unable to parse ca certificate: %w", err)
	}

	switch cert.Issuer.CommonName {
	case "kubernetes-ca":
		return true, nil
	case "kubernetes-front-proxy-ca":
		return true, nil
	case "etcd-ca":
		return true, nil
	case ca.Subject.CommonName:
		if file.Exists(filepath.Join(m.rootDir, "ca.key")) {
			return true, nil
		}
		logrus.Warnf("certificate issued by %q, but no ca.key found, not renewing the certificate %q", ca.Subject.CommonName, cert.Subject.CommonName)
		return false, nil
	}

	return false, nil
}

func (m *Manager) CreateKeyPair(name string, ownerID int) error {
	keyFile := filepath.Join(m.rootDir, name+".key")
	pubFile := filepath.Join(m.rootDir, name+".pub")

	if file.Exists(keyFile) && file.Exists(pubFile) {
		return file.Chown(keyFile, ownerID, constant.CertSecureMode)
	}

	reader := rand.Reader
	bitSize := 2048

	key, err := rsa.GenerateKey(reader, bitSize)
	if err != nil {
		return err
	}

	err = file.WriteAtomically(keyFile, constant.CertSecureMode, func(unbuffered io.Writer) error {
		privateKey := pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(key),
		}

		w := bufio.NewWriter(unbuffered)
		if err := pem.Encode(w, &privateKey); err != nil {
			return err
		}
		return w.Flush()
	})

	if err != nil {
		return err
	}

	return file.WriteAtomically(pubFile, 0644, func(unbuffered io.Writer) error {
		// note to the next reader: key.Public() != key.PublicKey
		pubBytes, err := x509.MarshalPKIXPublicKey(key.Public())
		if err != nil {
			return err
		}

		pemKey := pem.Block{
			Type:  "PUBLIC KEY",
			Bytes: pubBytes,
		}

		w := bufio.NewWriter(unbuffered)
		if err := pem.Encode(w, &pemKey); err != nil {
			return err
		}
		return w.Flush()
	})
}
