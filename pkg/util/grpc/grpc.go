/*
Copyright 2024 The Karmada Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package grpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Config the config of GRPC connection.
type Config struct {
	// InsecureSkipVerify controls whether a client verifies the server's
	// certificate chain and host name. If InsecureSkipVerify is true, crypto/tls
	// accepts any certificate presented by the server and any host name in that
	// certificate. In this mode, TLS is susceptible to machine-in-the-middle
	// attacks unless custom verification is used. This should be used only for
	// testing or in combination with VerifyConnection or VerifyPeerCertificate.
	InsecureSkipVerify bool
	// When this is set, server will check all incoming HTTPS requests for a client certificate signed by the trusted CA,
	// requests that don’t supply a valid client certificate will fail. If authentication is enabled,
	// the certificate provides credentials for the user name given by the Common Name field.
	ClientCertAuth bool
	// The secure port on which to serve gRPC.
	ServerPort int
	// Trusted certificate authority.
	TrustedCAFile string
	// Certificate used for SSL/TLS connections.
	CertFile string
	// Key for the certificate.
	KeyFile string
}

// NewServer creates a gRPC server which has no service registered and has not
// started to accept requests yet.
func (g *Config) NewServer() (*grpc.Server, error) {
	if g.CertFile == "" || g.KeyFile == "" {
		return grpc.NewServer(), nil
	}

	cert, err := tls.LoadX509KeyPair(g.CertFile, g.KeyFile)
	if err != nil {
		return nil, err
	}
	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS13,
	}
	if g.ClientCertAuth {
		config.ClientAuth = tls.RequireAndVerifyClientCert
		certPool := x509.NewCertPool()
		ca, err := os.ReadFile(g.TrustedCAFile)
		if err != nil {
			return nil, err
		}
		if ok := certPool.AppendCertsFromPEM(ca); !ok {
			return nil, fmt.Errorf("failed to append ca into certPool")
		}
		config.ClientCAs = certPool
	}

	return grpc.NewServer(grpc.Creds(grpccredentials.NewTLS(config))), nil
}

// DialWithTimeOut creates a client connection to the given target.
func (g *Config) DialWithTimeOut(path string, timeout time.Duration) (*grpc.ClientConn, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}

	var cred grpccredentials.TransportCredentials
	if g.TrustedCAFile == "" && !g.InsecureSkipVerify {
		// insecure connection
		cred = insecure.NewCredentials()
	} else {
		// server-side TLS
		config := &tls.Config{InsecureSkipVerify: g.InsecureSkipVerify} // nolint:gosec // G402: TLS InsecureSkipVerify may be true.
		if g.TrustedCAFile != "" {
			certPool := x509.NewCertPool()
			ca, err := os.ReadFile(g.TrustedCAFile)
			if err != nil {
				return nil, err
			}
			if ok := certPool.AppendCertsFromPEM(ca); !ok {
				return nil, fmt.Errorf("failed to append ca certs")
			}
			config.RootCAs = certPool
		}
		if g.CertFile != "" && g.KeyFile != "" {
			// mutual TLS
			certificate, err := tls.LoadX509KeyPair(g.CertFile, g.KeyFile)
			if err != nil {
				return nil, err
			}
			config.Certificates = []tls.Certificate{certificate}
		}
		cred = grpccredentials.NewTLS(config)
	}

	opts = append(opts, grpc.WithTransportCredentials(cred))
	cc, err := grpc.DialContext(ctx, path, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s error: %v", path, err)
	}

	return cc, nil
}
