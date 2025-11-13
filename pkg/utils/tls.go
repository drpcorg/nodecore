package utils

import (
	"crypto/x509"
	"os"
)

func GetCustomCAPool(caFilePath string) (*x509.CertPool, error) {
	if caFilePath == "" {
		return nil, nil
	}
	pem, err := os.ReadFile(caFilePath)
	if err != nil {
		return nil, err
	}

	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(pem)

	return caCertPool, nil
}
