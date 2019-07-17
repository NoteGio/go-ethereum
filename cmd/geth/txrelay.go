// Copyright 2015 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	"fmt"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/cmd/utils"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/ethereum/go-ethereum/ethdb/cdc"
	"github.com/Shopify/sarama"
	"gopkg.in/urfave/cli.v1"
	"os"
	"time"
	"log"
)

var (
	txrelayCommand = cli.Command{
		Action:    utils.MigrateFlags(txrelay), // keep track of migration progress
		Name:      "txrelay",
		Usage:     "Broadcast signed transactions from a Kafka topic",
		ArgsUsage: " ",
		Category:  "REPLICA COMMANDS",
		Description: `
Picks up transactions placed on Kafka topics by replicas an relays them to RPC
nodes.
`,
		Flags: []cli.Flag{
			utils.KafkaLogBrokerFlag,
			utils.KafkaTransactionTopicFlag,
			utils.KafkaTransactionConsumerGroupFlag,
		},
	}
)
// replica starts replica node
func txrelay(ctx *cli.Context) error {
	sarama.Logger = log.New(os.Stderr, "[sarama]", 0)
	rpcEndpoint := ctx.Args().First()
	if rpcEndpoint == "" {
		rpcEndpoint = fmt.Sprintf("%s/.ethereum/geth.ipc", os.Getenv("HOME"))
	}
	broker := ctx.GlobalString(utils.KafkaLogBrokerFlag.Name)
	topic := ctx.GlobalString(utils.KafkaTransactionTopicFlag.Name)
	consumerGroupID := ctx.GlobalString(utils.KafkaTransactionConsumerGroupFlag.Name)
	config := sarama.NewConfig()
	config.Version = sarama.V2_1_0_0
	if err := cdc.CreateTopicIfDoesNotExist(broker, topic); err != nil {
		fmt.Println("Error creating topic")
		return err
	}
	consumerGroup, err := sarama.NewConsumerGroup([]string{broker}, consumerGroupID, config)
	if err != nil {
		fmt.Printf("Looks like %v isn't available\n", broker)
		return err
	}
	defer consumerGroup.Close()
	conn, err := ethclient.Dial(rpcEndpoint)
	if err != nil {
		fmt.Println("Dial Error")
		return err
	}
	for {
		handler := relayConsumerGroup{conn}
		if err := consumerGroup.Consume(context.Background(), []string{topic}, handler); err != nil {
			return err
		}
		time.Sleep(500 * time.Millisecond)
	}
}


type relayConsumerGroup struct{
	txs ethereum.TransactionSender
}

func (relayConsumerGroup) Setup(_ sarama.ConsumerGroupSession) error   { return nil }
func (relayConsumerGroup) Cleanup(_ sarama.ConsumerGroupSession) error { return nil }
func (h relayConsumerGroup) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
  for msg := range claim.Messages() {
		transaction := &types.Transaction{}
		fmt.Printf("Msg: %v\n", msg)
		if err := rlp.DecodeBytes(msg.Value, transaction); err != nil {
			fmt.Printf("Error decoding: %v\n", err.Error())
		}
		if err := h.txs.SendTransaction(context.Background(), transaction); err != nil {
			fmt.Printf("Error Sending: %v\n", err.Error())
		}
    sess.MarkMessage(msg, "")
		fmt.Println("Processed a message\n")
  }
  return nil
}
