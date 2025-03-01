/**
 * Copyright 2020 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package dynamodb

import (
	"errors"
	"net/http"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/go-playground/validator/v10"
	"github.com/xmidt-org/argus/model"
	"github.com/xmidt-org/argus/store"
	"github.com/xmidt-org/argus/store/db/metric"
	"github.com/xmidt-org/httpaux/erraux"
)

// DynamoDB is the path to the configuration structure
// needed to connect to a dynamo DB instance.
const DynamoDB = "dynamo"

const (
	defaultTable      = "gifnoc"
	defaultMaxRetries = 3
)

var validate *validator.Validate

var errHTTPBadRequest = &erraux.Error{
	Err:  errors.New("bad request to dynamodb"),
	Code: http.StatusBadRequest,
}

func init() {
	validate = validator.New()
}

// Config contains all fields needed to establish a connection
// with a dynamoDB instance.
type Config struct {
	// Table is the name of the target DB table.
	// (Optional) Defaults to 'gifnoc'
	Table string

	// Endpoint is the HTTP(S) URL to the DB.
	// (Optional) Defaults to endpoint generated by the aws sdk.
	Endpoint string

	// Region is the AWS region of the running DB.
	Region string `validate:"required"`

	// MaxRetries is the number of times DB operations will be retried on error.
	// (Optional) Defaults to 3.
	MaxRetries int

	// AccessKey is the AWS AccessKey credential.
	AccessKey string `validate:"required"`

	// SecretKey is the AWS SecretKey credential.
	SecretKey string `validate:"required"`

	// DisableDualStack indicates whether the connection to the DB should be
	// dual stack (IPv4 and IPv6).
	// (Optional) Defaults to False.
	DisableDualStack bool
}

// dao adapts the underlying dynamodb data service to match
// the store.DAO (currently named store.S but we should rename it) interface.
type dao struct {
	s service
}

func NewDynamoDB(config Config, measures metric.Measures) (store.S, error) {
	if config.Table == "" {
		config.Table = defaultTable
	}
	if config.MaxRetries == 0 {
		config.MaxRetries = defaultMaxRetries
	}
	err := validate.Struct(config)
	if err != nil {
		return nil, err
	}

	awsConfig := *aws.NewConfig().
		WithEndpoint(config.Endpoint).
		WithUseDualStack(!config.DisableDualStack).
		WithMaxRetries(config.MaxRetries).
		WithCredentialsChainVerboseErrors(true).
		WithRegion(config.Region).
		WithCredentials(credentials.NewStaticCredentialsFromCreds(credentials.Value{
			AccessKeyID:     config.AccessKey,
			SecretAccessKey: config.SecretKey,
		}))

	svc, err := newService(awsConfig, "", config.Table)
	if err != nil {
		return nil, err
	}

	svc = newInstrumentingService(&dynamoMeasuresUpdater{measures: &measures}, svc, time.Now)
	return &dao{
		s: svc,
	}, nil
}

func (d dao) Push(key model.Key, item store.OwnableItem) error {
	_, err := d.s.Push(key, item)
	return sanitizeError(err)
}

func (d dao) Get(key model.Key) (store.OwnableItem, error) {
	item, _, err := d.s.Get(key)
	return item, sanitizeError(err)
}

func (d *dao) Delete(key model.Key) (store.OwnableItem, error) {
	item, _, err := d.s.Delete(key)
	return item, sanitizeError(err)
}

func (d *dao) GetAll(bucket string) (map[string]store.OwnableItem, error) {
	items, _, err := d.s.GetAll(bucket)
	return items, sanitizeError(err)
}

func sanitizeError(err error) error {
	if err == nil {
		return nil
	}
	var awsErr awserr.Error
	if errors.As(err, &awsErr) {
		if awsErr.Code() == "ValidationException" {
			return store.SanitizedError{Err: err, ErrHTTP: errHTTPBadRequest}
		}
	}
	return store.SanitizeError(err)
}
