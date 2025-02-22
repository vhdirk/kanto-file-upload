// Copyright (c) 2021 Contributors to the Eclipse Foundation
//
// See the NOTICE file(s) distributed with this work for additional
// information regarding copyright ownership.
//
// This program and the accompanying materials are made available under the
// terms of the Eclipse Public License 2.0 which is available at
// http://www.eclipse.org/legal/epl-2.0
//
// SPDX-License-Identifier: EPL-2.0

package uploaders

import (
	"crypto/md5"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/eclipse-kanto/file-upload/logger"
)

// Constants for HTTP(S) file upload 'start' operation options
const (
	StorageProviderHTTP = "generic"

	URLProp       = "https.url"
	MethodProp    = "https.method"
	HeadersPrefix = "https.header."
)

// ContentMD5 header name
const ContentMD5 = "Content-MD5"

const missingParameterErrMsg = "required parameter '%s' missing or empty"

// Uploader interface wraps the generic UploadFile method
type Uploader interface {
	UploadFile(file *os.File, useChecksum bool, listener func(bytesTransferred int64)) error
}

// HTTPUploader handles generic HTTP uploads
type HTTPUploader struct {
	url          string
	headers      map[string]string
	method       string
	serverCert   string
	cipherSuites []uint16
}

// NewHTTPUploader construct new HttpUploader from the provided 'start' operation options
func NewHTTPUploader(options map[string]string, serverCert string) (Uploader, error) {
	url := options[URLProp]
	if url == "" {
		return nil, errors.New("upload URL not specified")
	}

	method, ok := options[MethodProp]
	if !ok {
		method = "PUT"
	} else {
		method = strings.ToUpper(method)
	}

	if method != "PUT" && method != "POST" {
		return nil, fmt.Errorf("unsupported HTTP method: %s", method)
	}

	headers := ExtractDictionary(options, HeadersPrefix)

	return &HTTPUploader{url, headers, method, serverCert, SupportedCipherSuites()}, nil
}

func (u *HTTPUploader) getHTTPTransport() (*http.Transport, error) {
	var caCertPool *x509.CertPool
	if len(u.serverCert) > 0 {
		caCert, err := ioutil.ReadFile(u.serverCert)
		if err != nil {
			logger.Errorf("Error reading CA certificate file - \"%s\"", u.serverCert)
			return nil, err
		}
		caCertPool = x509.NewCertPool()
		caCertPool.AppendCertsFromPEM(caCert)
	}

	config := &tls.Config{ // using the system CA pool
		InsecureSkipVerify: false,
		RootCAs:            caCertPool,
		MinVersion:         tls.VersionTLS12,
		MaxVersion:         tls.VersionTLS13,
		CipherSuites:       u.cipherSuites,
	}
	return &http.Transport{
		TLSClientConfig: config,
	}, nil
}

// UploadFile performs generic HTTP file upload
func (u *HTTPUploader) UploadFile(file *os.File, useChecksum bool, listener func(bytesTransferred int64)) error {
	stats, err := file.Stat()
	if err != nil {
		return err
	}

	req, err := http.NewRequest(u.method, u.url, file)
	if err != nil {
		return err
	}

	parsedURL, _ := url.Parse(u.url) // MUST not return error, since http(s) request was done to that url
	transport := &http.Transport{}
	if parsedURL.Scheme == "https" {
		transport, err = u.getHTTPTransport()
		if err != nil {
			return err
		}
	}

	req.Header.Set("Content-Type", "application/x-binary")
	for name, value := range u.headers {
		req.Header.Set(name, value)
	}

	if useChecksum {
		md5, err := ComputeMD5(file, true)
		if err != nil {
			return err
		}
		req.Header.Set(ContentMD5, md5)
	}

	req.ContentLength = stats.Size()
	// Send the HTTP(S) request and get its response.
	client := &http.Client{Transport: transport}
	resp, err := client.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("upload failed - code: %d, status: %s", resp.StatusCode, resp.Status)
	}

	return nil
}

// ExtractDictionary extracts from the given map properties with a specified prefix.
// In the resulting dictionary, property names have the prefix removed.
func ExtractDictionary(options map[string]string, prefix string) map[string]string {
	info := make(map[string]string)

	for key, value := range options {
		if strings.HasPrefix(key, prefix) {
			newKey := strings.TrimPrefix(key, prefix)

			info[newKey] = value
		}
	}

	return info
}

// ComputeMD5 returns the MD5 hash of a file, which can be encoded as base64 string.
func ComputeMD5(f *os.File, encodeBase64 bool) (string, error) {
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}

	md5 := h.Sum(nil)

	f.Seek(0, 0)

	if !encodeBase64 {
		return string(md5), nil
	}
	encoded := base64.StdEncoding.EncodeToString(md5)

	return encoded, nil
}

// SupportedCipherSuites returns the ids of secure TLS cipher suites
func SupportedCipherSuites() []uint16 {
	cs := tls.CipherSuites()
	cid := make([]uint16, len(cs))
	for i := range cs {
		cid[i] = cs[i].ID
	}
	return cid
}
