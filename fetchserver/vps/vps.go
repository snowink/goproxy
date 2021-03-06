package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bradfitz/http2"
	// "github.com/cloudflare/golibs/lrucache"
	"github.com/golang/glog"
)

var (
	PORT      string          = os.Getenv("PORT")
	PASSWROD  string          = os.Getenv("PASSWORD")
	transport *http.Transport = &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true,
			ClientSessionCache: tls.NewLRUClientSessionCache(1000),
		},
		TLSHandshakeTimeout: 30 * time.Second,
		MaxIdleConnsPerHost: 4,
		DisableCompression:  false,
	}
)

type listener struct {
	net.Listener
}

func (l *listener) Accept() (c net.Conn, err error) {
	c, err = l.Listener.Accept()
	if err != nil {
		return
	}

	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(3 * time.Minute)
	}

	return
}

func getCertificate(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	fmt.Printf("getCertificate(%#v)", clientHello)
	// name := clientHello.ServerName
	name := "www.gov.cn"
	glog.Infof("Generating RootCA for %s", name)
	template := x509.Certificate{
		IsCA:         true,
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{name},
		},
		NotBefore: time.Now().Add(-time.Duration(5 * time.Minute)),
		NotAfter:  time.Now().Add(180 * 24 * time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	priv, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		return nil, err
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}

	certPEMBlock := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyPEMBlock := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)})

	cert, err := tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	return &cert, err
}

func handler(rw http.ResponseWriter, req *http.Request) {
	var err error

	glog.Infof("%s \"%s %s %s\" - -", req.RemoteAddr, req.Method, req.URL.String(), req.Proto)

	var paramsPreifx string = http.CanonicalHeaderKey("X-UrlFetch-")
	params := map[string]string{}
	for key, values := range req.Header {
		if strings.HasPrefix(key, paramsPreifx) {
			params[strings.ToLower(key[len(paramsPreifx):])] = values[0]
		}
	}

	for _, key := range params {
		req.Header.Del(paramsPreifx + key)
	}

	if PASSWROD != "" {
		if password, ok := params["password"]; !ok || password != PASSWROD {
			http.Error(rw, fmt.Sprintf("wrong password %#v", password), http.StatusForbidden)
			return
		}
	}

	resp, err := transport.RoundTrip(req)
	if err != nil {
		http.Error(rw, err.Error(), http.StatusBadGateway)
		return
	}

	for key, values := range resp.Header {
		for _, value := range values {
			rw.Header().Add(key, value)
		}
	}
	rw.WriteHeader(http.StatusOK)
	io.Copy(rw, resp.Body)
}

func main() {
	flag.Parse()

	ln, err := net.Listen("tcp", ":"+PORT)
	if err != nil {
		glog.Fatalf("Listen(%s) error: %s", PORT, err)
	}

	cert, err := getCertificate(nil)
	if err != nil {
		glog.Fatalf("getCertificate error: %s", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{*cert},
		// GetCertificate: getCertificate,
	}

	s := &http.Server{
		Handler:   http.HandlerFunc(handler),
		TLSConfig: tlsConfig,
	}

	http2.ConfigureServer(s, &http2.Server{})
	glog.Infof("ListenAndServe on %s\n", ln.Addr().String())
	s.Serve(tls.NewListener(ln, tlsConfig))
}
