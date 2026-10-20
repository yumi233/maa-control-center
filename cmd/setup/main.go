// setup generates a private CA and credentials without printing secrets.
package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

func secret() string {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func save(path string, b []byte) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	must(err)
	_, err = f.Write(b)
	must(err)
	must(f.Close())
}
func main() {
	dir := flag.String("dir", "private", "new private configuration directory")
	host := flag.String("host", "localhost", "actual NAS DNS name or private IP")
	port := flag.String("port", "18443", "new unoccupied HTTPS port")
	container := flag.Bool("container", false, "container-internal paths")
	flag.Parse()
	must(os.MkdirAll(*dir, 0700))
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(err)
	serial := func() *big.Int {
		n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
		must(err)
		return n
	}
	ca := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "MAA Control Center Private CA"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(3, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, ca, ca, &caKey.PublicKey, caKey)
	must(err)
	leaf := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: *host}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().AddDate(1, 0, 0), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, DNSNames: []string{"localhost"}}
	if ip := net.ParseIP(*host); ip != nil {
		leaf.IPAddresses = append(leaf.IPAddresses, ip)
	} else {
		leaf.DNSNames = append(leaf.DNSNames, *host)
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leaf, ca, &leafKey.PublicKey, caKey)
	must(err)
	keyDER, err := x509.MarshalPKCS8PrivateKey(leafKey)
	must(err)
	caKeyDER, err := x509.MarshalPKCS8PrivateKey(caKey)
	must(err)
	save(filepath.Join(*dir, "ca.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}))
	save(filepath.Join(*dir, "ca-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: caKeyDER}))
	save(filepath.Join(*dir, "server.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}))
	save(filepath.Join(*dir, "server-key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}))
	password, agentToken := secret(), secret()
	certPath, _ := filepath.Abs(*dir)
	config := map[string]string{"MAACC_PUBLIC_URL": "https://" + net.JoinHostPort(*host, *port), "MAACC_LISTEN": "0.0.0.0:" + *port, "MAACC_DB": "data/control-center.db", "MAACC_ADMIN_PASSWORD": password, "MAACC_AGENT_ID": "windows-agent", "MAACC_AGENT_TOKEN": agentToken, "MAACC_TLS_CERT": filepath.Join(certPath, "server.pem"), "MAACC_TLS_KEY": filepath.Join(certPath, "server-key.pem")}
	if *container {
		config["MAACC_DB"] = "/data/control-center.db"
		config["MAACC_TLS_CERT"] = "/config/server.pem"
		config["MAACC_TLS_KEY"] = "/config/server-key.pem"
	}
	b, err := json.MarshalIndent(config, "", "  ")
	must(err)
	save(filepath.Join(*dir, "server.private.json"), b)
	save(filepath.Join(*dir, "admin.txt"), []byte("MAA Control Center\nURL: "+config["MAACC_PUBLIC_URL"]+"\nUsername: admin\nPassword: "+password+"\nKeep this file private. No agent token is included here.\n"))
	fmt.Println("Initialized TLS and credentials. Login details are in the private directory's admin.txt. No secrets were printed.")
}
