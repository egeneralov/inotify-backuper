package main

import (
	"io/ioutil"
	"os"

	"gopkg.in/yaml.v2"

	log "github.com/sirupsen/logrus"
)

type cfg struct {
	S3 struct {
		Endpoint        string `yaml:"endpoint"`
		AccessKeyID     string `yaml:"accessKeyID"`
		SecretAccessKey string `yaml:"secretAccessKey"`
		UseSSL          bool   `yaml:"ssl"`
		Bucket          string `yaml:"bucket"`
		StorageClass    string `yaml:"storage_class"`
		ContentType     string `yaml:"content_type"`
	} `yaml:"s3"`
	Notify struct {
		//Files []string `yaml:"files"`
		Match struct {
			Names []string `yaml:"names"`
		} `yaml:"match"`
	} `yaml:"notify"`
	Metrics struct {
		Namespace string `yaml:"namespace"`
	} `yaml:"metrics"`
}

const (
	envEndpoint        = "S3_ENDPOINT"
	envAccessKeyID     = "S3_ACCESS_KEY"
	envSecretAccessKey = "S3_SECRET_KEY"
	envSSL             = "S3_SSL"
	envBucket          = "S3_BUCKET"
	envStorageClass    = "S3_STORAGE_CLASS"
	envContentType     = "S3_CONTENT_TYPE"
)

var (
	configuration = &cfg{
		S3: struct {
			Endpoint        string `yaml:"endpoint"`
			AccessKeyID     string `yaml:"accessKeyID"`
			SecretAccessKey string `yaml:"secretAccessKey"`
			UseSSL          bool   `yaml:"ssl"`
			Bucket          string `yaml:"bucket"`
			StorageClass    string `yaml:"storage_class"`
			ContentType     string `yaml:"content_type"`
		}{
			Endpoint:        os.Getenv(envEndpoint),
			AccessKeyID:     os.Getenv(envAccessKeyID),
			SecretAccessKey: os.Getenv(envSecretAccessKey),
			UseSSL:          true,
			Bucket:          os.Getenv(envBucket),
			StorageClass:    os.Getenv(envStorageClass),
			ContentType:     os.Getenv(envContentType),
		},
	}
)

func loadConfig(path string) (*cfg, error) {
	if os.Getenv(envSSL) == "false" {
		configuration.S3.UseSSL = false
	}
	dat, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	err = yaml.Unmarshal(dat, &configuration)
	if err != nil {
		log.Fatal(err)
	}

	return configuration, nil
}
