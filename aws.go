/* ****************************************************************************
 * Copyright 2020 51 Degrees Mobile Experts Limited (51degrees.com)
 *
 * Licensed under the Apache License, Version 2.0 (the "License"); you may not
 * use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS, WITHOUT
 * WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the
 * License for the specific language governing permissions and limitations
 * under the License.
 * ***************************************************************************/

package swift

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/dynamodb"
	"github.com/aws/aws-sdk-go/service/dynamodb/dynamodbattribute"
)

// AWS is a implementation of sws.Store for AWS DynamoDB.
type AWS struct {
	timestamp time.Time          // The last time the maps were refreshed
	svc       *dynamodb.DynamoDB // Reference to the creators table
	common
}

// NodeItem is the dynamodb table item representation of a node
type NodeItem struct {
	Network      string    // The name of the network the node belongs to
	Domain       string    // The domain name associated with the node
	Created      time.Time // The time that the node first came online
	Expires      time.Time // The time that the node will retire from the network
	Role         int       // The role the node has in the network
	ScramblerKey string    // Secret used to scramble data with fixed nonce
}

// SecretItem is the dynamodb table item representation of a secret
type SecretItem struct {
	Domain       string
	TimeStamp    time.Time
	ScramblerKey string
}

// NewAWS creates a new instance of the AWS structure
func NewAWS(region string) (*AWS, error) {
	var a AWS
	var sess *session.Session

	// Configure session with credentials from .aws/credentials and region...
	if region != "" {
		// ... from region param
		config := &aws.Config{
			Region: aws.String(region),
		}
		sess = session.Must(session.NewSession(config))
	} else {
		// ... from .aws/config
		sess = session.Must(session.NewSessionWithOptions(session.Options{
			SharedConfigState: session.SharedConfigEnable,
		}))
	}
	if sess == nil {
		return nil, errors.New("AWS session is nil")
	}
	a.svc = dynamodb.New(sess)

	_, err := a.awsCreateTables()
	if err != nil {
		return nil, err
	}

	a.mutex = &sync.Mutex{}
	err = a.refresh()
	if err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *AWS) awsCreateTables() (bool, error) {

	_, err := a.createNodesTable()
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case dynamodb.ErrCodeTableAlreadyExistsException:
				break
			case dynamodb.ErrCodeResourceInUseException:
				break
			default:
				return false, err
			}
		} else {
			return false, err
		}
	}

	_, err = a.createSecretsTable()
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case dynamodb.ErrCodeTableAlreadyExistsException:
				break
			case dynamodb.ErrCodeResourceInUseException:
				break
			default:
				return false, err
			}
		} else {
			return false, err
		}
	}

	return true, nil
}

func (a *AWS) createNodesTable() (*dynamodb.CreateTableOutput, error) {

	// Create nodes table
	nodesTableInput := &dynamodb.CreateTableInput{
		AttributeDefinitions: []*dynamodb.AttributeDefinition{
			{
				AttributeName: aws.String(networkFieldName),
				AttributeType: aws.String("S"),
			},
			{
				AttributeName: aws.String(domainFieldName),
				AttributeType: aws.String("S"),
			},
		},
		KeySchema: []*dynamodb.KeySchemaElement{
			{
				AttributeName: aws.String(networkFieldName),
				KeyType:       aws.String("HASH"),
			},
			{
				AttributeName: aws.String(domainFieldName),
				KeyType:       aws.String("RANGE"),
			},
		},
		BillingMode: aws.String("PAY_PER_REQUEST"),
		TableName:   aws.String(nodesTableName),
	}
	return a.svc.CreateTable(nodesTableInput)
}

func (a *AWS) createSecretsTable() (*dynamodb.CreateTableOutput, error) {
	// Create secrets table
	secretsTableInput := &dynamodb.CreateTableInput{
		AttributeDefinitions: []*dynamodb.AttributeDefinition{
			{
				AttributeName: aws.String(domainFieldName),
				AttributeType: aws.String("S"),
			},
			{
				AttributeName: aws.String(scramblerKeyFieldName),
				AttributeType: aws.String("S"),
			},
		},
		KeySchema: []*dynamodb.KeySchemaElement{
			{
				AttributeName: aws.String(domainFieldName),
				KeyType:       aws.String("HASH"),
			},
			{
				AttributeName: aws.String(scramblerKeyFieldName),
				KeyType:       aws.String("RANGE"),
			},
		},
		BillingMode: aws.String("PAY_PER_REQUEST"),
		TableName:   aws.String(secretsTableName),
	}
	return a.svc.CreateTable(secretsTableInput)
}

// GetNode takes a domain name and returns the associated node. If a node
// does not exist then nil is returned.
func (a *AWS) getNode(domain string) (*node, error) {
	n, err := a.common.getNode(domain)
	if err != nil {
		return nil, err
	}
	if n == nil {
		err = a.refresh()
		if err != nil {
			return nil, err
		}
		n, err = a.common.getNode(domain)
	}
	return n, err
}

// GetNodes returns all the nodes associated with a network.
func (a *AWS) getNodes(network string) (*nodes, error) {
	ns, err := a.common.getNodes(network)
	if err != nil {
		return nil, err
	}
	if ns == nil {
		err = a.refresh()
		if err != nil {
			return nil, err
		}
		ns, err = a.common.getNodes(network)
	}
	return ns, err
}

// SetNode inserts or updates the node.
func (a *AWS) setNode(node *node) error {
	err := a.setNodeSecrets(node)
	if err != nil {
		return err
	}
	item := NodeItem{
		node.network,
		node.domain,
		node.created,
		node.expires,
		node.role,
		node.scrambler.key}

	av, err := dynamodbattribute.MarshalMap(item)
	if err != nil {
		fmt.Println("Got error marshalling new creator item:")
		return err
	}

	input := &dynamodb.PutItemInput{
		Item:      av,
		TableName: aws.String(nodesTableName),
	}

	_, err = a.svc.PutItem(input)
	if err != nil {
		fmt.Println("Got error calling PutItem:")
		return err
	}

	return nil
}

func (a *AWS) refresh() error {
	nets := make(map[string]*nodes)

	// Fetch the nodes and then add the secrets.
	ns, err := a.fetchNodes()
	if err != nil {
		return err
	}
	err = a.addSecrets(ns)
	if err != nil {
		return err
	}

	// Create a map of networks from the nodes found.
	for _, v := range ns {
		net := nets[v.network]
		if net == nil {
			net = &nodes{}
			net.dict = make(map[string]*node)
			nets[v.network] = net
		}
		net.all = append(net.all, v)
		net.dict[v.domain] = v
	}

	// Finally sort the nodes by hash values and whether they are active.
	for _, net := range nets {
		net.order()
	}

	// In a single atomic operation update the reference to the networks and
	// nodes.
	a.mutex.Lock()
	a.nodes = ns
	a.networks = nets
	a.mutex.Unlock()

	return nil
}

func (a *AWS) fetchNodes() (map[string]*node, error) {
	var err error
	ns := make(map[string]*node)

	params := &dynamodb.ScanInput{
		TableName: aws.String(nodesTableName),
	}

	// Fetch all the records from the nodes table in Dynamo.
	result, err := a.svc.Scan(params)
	if err != nil {
		fmt.Println("Query API call failed:")
		fmt.Println((err.Error()))
		return nil, err
	}

	// Iterate over the records creating nodes and adding them to the networks
	// map.
	for _, i := range result.Items {
		nodeItem := NodeItem{}

		err = dynamodbattribute.UnmarshalMap(i, &nodeItem)
		if err != nil {
			fmt.Println("Got error unmarshalling:")
			fmt.Println(err.Error())
			return nil, err
		}

		ns[nodeItem.Domain], err = newNode(
			nodeItem.Network,
			nodeItem.Domain,
			nodeItem.Created,
			nodeItem.Expires,
			nodeItem.Role,
			nodeItem.ScramblerKey)
		if err != nil {
			return nil, err
		}
	}

	return ns, err
}

func (a *AWS) addSecrets(ns map[string]*node) error {

	// Fetch all the records from the secrets table in Azure.
	params := &dynamodb.ScanInput{
		TableName: aws.String(nodesTableName),
	}

	result, err := a.svc.Scan(params)
	if err != nil {
		fmt.Println("Query API call failed:")
		fmt.Println((err.Error()))
		return err
	}

	// Iterate over the secrets adding them to nodes.
	for _, i := range result.Items {
		secretItem := SecretItem{}

		err = dynamodbattribute.UnmarshalMap(i, &secretItem)
		if err != nil {
			fmt.Println("Got error unmarshalling:")
			fmt.Println(err.Error())
			return err
		}

		s, err := newSecretFromKey(secretItem.ScramblerKey, secretItem.TimeStamp)
		if err != nil {
			return err
		}
		if ns[secretItem.Domain] != nil {
			ns[secretItem.Domain].addSecret(s)
		}
	}

	// Sort the secrets so the most recent is at the start of the array.
	for _, n := range ns {
		n.sortSecrets()
	}

	return nil
}

func (a *AWS) setNodeSecrets(node *node) error {
	var pi []*dynamodb.WriteRequest

	for _, s := range node.secrets {
		item := SecretItem{
			node.domain,
			s.timeStamp,
			s.key}

		av, err := dynamodbattribute.MarshalMap(item)
		if err != nil {
			fmt.Println("Got error marshalling new creator item:")
			return err
		}

		pi = append(pi, &dynamodb.WriteRequest{
			PutRequest: &dynamodb.PutRequest{
				Item: av,
			},
		})
	}

	input := &dynamodb.BatchWriteItemInput{
		RequestItems: map[string][]*dynamodb.WriteRequest{
			secretsTableName: pi,
		},
	}

	_, err := a.svc.BatchWriteItem(input)
	if err != nil {
		return err
	}
	return nil
}
