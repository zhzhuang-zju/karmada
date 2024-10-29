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

package grpcconnection

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"google.golang.org/grpc"
	grpccredentials "google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	utilerrors "k8s.io/apimachinery/pkg/util/errors"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apiserver/pkg/server/dynamiccertificates"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	"github.com/karmada-io/karmada/pkg/util"
)

type GRPCServer interface {
	NewServer() error
	Serve(stopCh <-chan struct{}) error
}

// ServerConfig the config of GRPC server side.
type ServerConfig struct {
	// ServerPort The secure port on which to serve gRPC.
	ServerPort int
	// InsecureSkipClientVerify Controls whether verifies the client's certificate chain and host name.
	// When this is set to false, server will check all incoming HTTPS requests for a client certificate signed by the trusted CA,
	// requests that don’t supply a valid client certificate will fail. If authentication is enabled,
	// the certificate provides credentials for the user name given by the Common Name field.
	InsecureSkipClientVerify bool
	// ClientAuthCAFile SSL Certificate Authority file used to verify grpc client certificates on incoming requests.
	ClientAuthCAFile string
	// CertFile SSL certification file used for grpc SSL/TLS connections.
	CertFile string
	// KeyFile SSL key file used for grpc SSL/TLS connections.
	KeyFile string

	DynamicEnabled bool
}

// NewServer creates a gRPC server which has no service registered and has not
// started to accept requests yet.
func (s *ServerConfig) newServer(servingCertProvider *dynamiccertificates.DynamicCertKeyPairContent) (*grpc.Server, error) {
	if servingCertProvider == nil {
		return grpc.NewServer(), nil
	}

	config := &tls.Config{
		MinVersion: tls.VersionTLS13,
	}

	config.GetCertificate = func(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
		cert, key := servingCertProvider.CurrentCertKeyContent()
		certKeyPair, err := tls.X509KeyPair(cert, key)
		return &certKeyPair, err
	}

	if s.ClientAuthCAFile != "" {
		certPool := x509.NewCertPool()
		ca, err := os.ReadFile(s.ClientAuthCAFile)
		if err != nil {
			return nil, err
		}
		if ok := certPool.AppendCertsFromPEM(ca); !ok {
			return nil, fmt.Errorf("failed to append ca into certPool")
		}
		config.ClientCAs = certPool
		if !s.InsecureSkipClientVerify {
			config.ClientAuth = tls.RequireAndVerifyClientCert
		}
	}

	return grpc.NewServer(grpc.Creds(grpccredentials.NewTLS(config))), nil
}

func (s *ServerConfig) IsDynamic() bool {
	return s.CertFile != "" && s.KeyFile != "" && s.DynamicEnabled
}

type StaticServerConfig struct {
	*ServerConfig

	ServiceRegisterFunc  func(s *grpc.Server)
	NetListenerGenerator func() (net.Listener, error)
	Server               *grpc.Server
}

func (s *StaticServerConfig) NewServer() error {
	var servingCertProvider *dynamiccertificates.DynamicCertKeyPairContent
	var err error
	if s.CertFile != "" && s.KeyFile != "" {
		servingCertProvider, err = dynamiccertificates.NewDynamicServingContentFromFiles("serving grpc", s.CertFile, s.KeyFile)
		if err != nil {
			return fmt.Errorf("loading tls config (%s, %s) failed - %s", s.CertFile, s.KeyFile, err)
		}
	}
	s.Server, err = s.newServer(servingCertProvider)

	return err
}

func (s *StaticServerConfig) Serve(stopCh <-chan struct{}) error {
	// Graceful stop when the context is cancelled.
	go func() {
		<-stopCh
		s.Server.GracefulStop()
	}()

	s.ServiceRegisterFunc(s.Server)
	l, err := s.NetListenerGenerator()
	if err != nil {
		return err
	}
	defer l.Close()

	return s.Server.Serve(l)
}

type DynamicServerConfig struct {
	*ServerConfig

	ServingCertProvider *dynamiccertificates.DynamicCertKeyPairContent
	sync.RWMutex
	Server *grpc.Server

	ServiceRegisterFunc  func(s *grpc.Server)
	NetListenerGenerator func() (net.Listener, error)
	Queue                workqueue.RateLimitingInterface

	ErrChan chan error
}

// ClientConfig the config of GRPC client side.
type ClientConfig struct {
	// TargetPort the target port on which to establish a gRPC connection.
	TargetPort int
	// InsecureSkipServerVerify controls whether a client verifies the server's
	// certificate chain and host name. If InsecureSkipServerVerify is true, crypto/tls
	// accepts any certificate presented by the server and any host name in that
	// certificate. In this mode, TLS is susceptible to machine-in-the-middle
	// attacks unless custom verification is used. This should be used only for
	// testing or in combination with VerifyConnection or VerifyPeerCertificate.
	InsecureSkipServerVerify bool
	// ServerAuthCAFile SSL Certificate Authority file used to verify grpc server certificates.
	ServerAuthCAFile string
	// SSL certification file used for grpc SSL/TLS connections.
	CertFile string
	// SSL key file used for grpc SSL/TLS connections.
	KeyFile string
}

func (d *DynamicServerConfig) NewServer() error {
	var err error
	if d.CertFile != "" && d.KeyFile != "" && d.ServingCertProvider == nil {
		d.ServingCertProvider, err = dynamiccertificates.NewDynamicServingContentFromFiles("serving grpc", d.CertFile, d.KeyFile)
		if err != nil {
			return fmt.Errorf("loading tls config (%s, %s) failed - %s", d.CertFile, d.KeyFile, err)
		}
	}

	d.Server, err = d.ServerConfig.newServer(d.ServingCertProvider)
	if err != nil {
		return err
	}

	return nil
}

func (d *DynamicServerConfig) Serve(stopCh <-chan struct{}) error {
	return d.serveWithDynamicCerts(stopCh)
}

func (d *DynamicServerConfig) serveWithDynamicCerts(stopCh <-chan struct{}) error {
	d.ServingCertProvider.AddListener(d)

	go func() {
		err := d.serve()
		if err != nil {
			d.ErrChan <- err
		}
	}()

	ctx, cancel := util.ContextForChannel(stopCh)
	go wait.UntilWithContext(ctx, d.dynamicCertLoader, time.Second)
	go d.ServingCertProvider.Run(ctx, 1)

	for {
		select {
		case err := <-d.ErrChan:
			cancel()
			return err
		case <-stopCh:
			return nil
		}
	}
}

func (d *DynamicServerConfig) serve() error {
	d.ServiceRegisterFunc(d.Server)
	lis, err := d.NetListenerGenerator()
	if err != nil {
		return err
	}
	defer lis.Close()

	if err = d.Server.Serve(lis); err != nil {
		return err
	}

	return nil
}

func (d *DynamicServerConfig) Enqueue() {
	d.Queue.Add(struct{}{})
}

func (d *DynamicServerConfig) dynamicCertLoader(ctx context.Context) {
	for d.processNext(ctx) {
	}
}

func (d *DynamicServerConfig) processNext(_ context.Context) bool {
	key, shutdown := d.Queue.Get()
	if shutdown {
		klog.Errorf("Fail to pop item from Queue")
		return false
	}
	defer d.Queue.Done(key)

	d.Lock()
	defer d.Unlock()

	d.Server.GracefulStop()
	err := d.NewServer()
	if err != nil {
		d.ErrChan <- err
		return false
	}

	go func() {
		if err = d.serve(); err != nil {
			d.ErrChan <- err
		}
	}()

	return true
}

// DialWithTimeOut will attempt to create a client connection based on the given targets, one at a time, until a client connection is successfully established.
func (c *ClientConfig) DialWithTimeOut(paths []string, timeout time.Duration) (*grpc.ClientConn, error) {
	opts := []grpc.DialOption{
		grpc.WithBlock(),
	}

	var cred grpccredentials.TransportCredentials
	if c.ServerAuthCAFile == "" && !c.InsecureSkipServerVerify {
		// insecure connection
		cred = insecure.NewCredentials()
	} else {
		// server-side TLS
		config := &tls.Config{InsecureSkipVerify: c.InsecureSkipServerVerify} // nolint:gosec // G402: TLS InsecureSkipEstimatorVerify may be true.
		if c.ServerAuthCAFile != "" {
			certPool := x509.NewCertPool()
			ca, err := os.ReadFile(c.ServerAuthCAFile)
			if err != nil {
				return nil, err
			}
			if ok := certPool.AppendCertsFromPEM(ca); !ok {
				return nil, fmt.Errorf("failed to append ca certs")
			}
			config.RootCAs = certPool
		}
		if c.CertFile != "" && c.KeyFile != "" {
			// mutual TLS
			certificate, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
			if err != nil {
				return nil, err
			}
			config.Certificates = []tls.Certificate{certificate}
		}
		cred = grpccredentials.NewTLS(config)
	}

	opts = append(opts, grpc.WithTransportCredentials(cred))

	var cc *grpc.ClientConn
	var err error
	var allErrs []error
	for _, path := range paths {
		cc, err = createGRPCConnection(path, timeout, opts...)
		if err == nil {
			return cc, nil
		}
		allErrs = append(allErrs, err)
	}

	return nil, utilerrors.NewAggregate(allErrs)
}

func createGRPCConnection(path string, timeout time.Duration, opts ...grpc.DialOption) (conn *grpc.ClientConn, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cc, err := grpc.DialContext(ctx, path, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s error: %v", path, err)
	}

	return cc, nil
}
