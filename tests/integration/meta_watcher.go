// Licensed to the LF AI & Data foundation under one
// or more contributor license agreements. See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership. The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License. You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"time"

	"github.com/golang/protobuf/proto"
	clientv3 "go.etcd.io/etcd/client/v3"
	"go.uber.org/zap"

	"github.com/milvus-io/milvus/internal/proto/datapb"
	"github.com/milvus-io/milvus/internal/proto/querypb"
	"github.com/milvus-io/milvus/internal/util/sessionutil"
	"github.com/milvus-io/milvus/pkg/log"
)

// MetaWatcher to observe meta data of milvus cluster
type MetaWatcher interface {
	ShowSessions() ([]*sessionutil.Session, error)
	ShowSegments() ([]*datapb.SegmentInfo, error)
	ShowReplicas() ([]*querypb.Replica, error)
}

type EtcdMetaWatcher struct {
	MetaWatcher
	rootPath string
	etcdCli  *clientv3.Client
}

func (watcher *EtcdMetaWatcher) ShowSessions() ([]*sessionutil.Session, error) {
	metaPath := watcher.rootPath + "/meta/session"
	return listSessionsByPrefix(watcher.etcdCli, metaPath)
}

func (watcher *EtcdMetaWatcher) ShowSegments() ([]*datapb.SegmentInfo, error) {
	metaBasePath := path.Join(watcher.rootPath, "/meta/datacoord-meta/s/") + "/"
	return listSegments(watcher.etcdCli, metaBasePath, func(s *datapb.SegmentInfo) bool {
		return true
	})
}

func (watcher *EtcdMetaWatcher) ShowReplicas() ([]*querypb.Replica, error) {
	metaBasePath := path.Join(watcher.rootPath, "/meta/querycoord-replica/")
	return listReplicas(watcher.etcdCli, metaBasePath)
}

//=================== Below largely copied from birdwatcher ========================

// listSessions returns all session
func listSessionsByPrefix(cli *clientv3.Client, prefix string) ([]*sessionutil.Session, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	sessions := make([]*sessionutil.Session, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		session := &sessionutil.Session{}
		err := json.Unmarshal(kv.Value, session)
		if err != nil {
			continue
		}

		sessions = append(sessions, session)
	}
	return sessions, nil
}

func listSegments(cli *clientv3.Client, prefix string, filter func(*datapb.SegmentInfo) bool) ([]*datapb.SegmentInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}
	segments := make([]*datapb.SegmentInfo, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		info := &datapb.SegmentInfo{}
		err = proto.Unmarshal(kv.Value, info)
		if err != nil {
			continue
		}
		if filter == nil || filter(info) {
			segments = append(segments, info)
		}
	}

	sort.Slice(segments, func(i, j int) bool {
		return segments[i].GetID() < segments[j].GetID()
	})
	return segments, nil
}

func listReplicas(cli *clientv3.Client, prefix string) ([]*querypb.Replica, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*3)
	defer cancel()
	resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	replicas := make([]*querypb.Replica, 0, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		replica := &querypb.Replica{}
		if err := proto.Unmarshal(kv.Value, replica); err != nil {
			log.Warn("failed to unmarshal replica info", zap.Error(err))
			continue
		}
		replicas = append(replicas, replica)
	}

	return replicas, nil
}

func PrettyReplica(replica *querypb.Replica) string {
	res := fmt.Sprintf("ReplicaID: %d CollectionID: %d\n", replica.ID, replica.CollectionID)
	res = res + fmt.Sprintf("Nodes:%v\n", replica.Nodes)
	return res
}
