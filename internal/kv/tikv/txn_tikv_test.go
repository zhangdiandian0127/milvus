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

package tikv

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/cockroachdb/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tikv/client-go/v2/txnkv"
	"github.com/tikv/client-go/v2/txnkv/transaction"
	"golang.org/x/exp/maps"

	"github.com/milvus-io/milvus/internal/kv/predicates"
)

func TestTiKVLoad(te *testing.T) {
	te.Run("kv SaveAndLoad", func(t *testing.T) {
		rootPath := "/tikv/test/root/saveandload"
		kv := NewTiKV(txnClient, rootPath)
		err := kv.RemoveWithPrefix("")
		require.NoError(t, err)

		defer kv.Close()
		defer kv.RemoveWithPrefix("")

		saveAndLoadTests := []struct {
			key   string
			value string
		}{
			{"test1", "value1"},
			{"test2", "value2"},
			{"test1/a", "value_a"},
			{"test1/b", "value_b"},
		}

		for i, test := range saveAndLoadTests {
			if i < 4 {
				err = kv.Save(test.key, test.value)
				assert.NoError(t, err)
			}

			val, err := kv.Load(test.key)
			assert.NoError(t, err)
			assert.Equal(t, test.value, val)
		}

		invalidLoadTests := []struct {
			invalidKey string
		}{
			{"t"},
			{"a"},
			{"test1a"},
		}

		for _, test := range invalidLoadTests {
			val, err := kv.Load(test.invalidKey)
			assert.Error(t, err)
			assert.Zero(t, val)
		}

		loadPrefixTests := []struct {
			prefix string

			expectedKeys   []string
			expectedValues []string
			expectedError  error
		}{
			{"test", []string{
				kv.GetPath("test1"),
				kv.GetPath("test2"),
				kv.GetPath("test1/a"),
				kv.GetPath("test1/b"),
			}, []string{"value1", "value2", "value_a", "value_b"}, nil},
			{"test1", []string{
				kv.GetPath("test1"),
				kv.GetPath("test1/a"),
				kv.GetPath("test1/b"),
			}, []string{"value1", "value_a", "value_b"}, nil},
			{"test2", []string{kv.GetPath("test2")}, []string{"value2"}, nil},
			{"", []string{
				kv.GetPath("test1"),
				kv.GetPath("test2"),
				kv.GetPath("test1/a"),
				kv.GetPath("test1/b"),
			}, []string{"value1", "value2", "value_a", "value_b"}, nil},
			{"test1/a", []string{kv.GetPath("test1/a")}, []string{"value_a"}, nil},
			{"a", []string{}, []string{}, nil},
			{"root", []string{}, []string{}, nil},
			{"/tikv/test/root", []string{}, []string{}, nil},
		}

		for _, test := range loadPrefixTests {
			actualKeys, actualValues, err := kv.LoadWithPrefix(test.prefix)
			assert.ElementsMatch(t, test.expectedKeys, actualKeys)
			assert.ElementsMatch(t, test.expectedValues, actualValues)
			assert.Equal(t, test.expectedError, err)
		}

		removeTests := []struct {
			validKey   string
			invalidKey string
		}{
			{"test1", "abc"},
			{"test1/a", "test1/lskfjal"},
			{"test1/b", "test1/b"},
			{"test2", "-"},
		}

		for _, test := range removeTests {
			err = kv.Remove(test.validKey)
			assert.NoError(t, err)

			_, err = kv.Load(test.validKey)
			assert.Error(t, err)

			err = kv.Remove(test.validKey)
			assert.NoError(t, err)
			err = kv.Remove(test.invalidKey)
			assert.NoError(t, err)
		}
	})

	te.Run("kv MultiSaveAndMultiLoad", func(t *testing.T) {
		rootPath := "/tikv/test/root/multi_save_and_multi_load"
		kv := NewTiKV(txnClient, rootPath)

		defer kv.Close()
		defer kv.RemoveWithPrefix("")

		multiSaveTests := map[string]string{
			"key_1":      "value_1",
			"key_2":      "value_2",
			"key_3/a":    "value_3a",
			"multikey_1": "multivalue_1",
			"multikey_2": "multivalue_2",
			"_":          "other",
		}

		err := kv.MultiSave(multiSaveTests)
		assert.NoError(t, err)
		for k, v := range multiSaveTests {
			actualV, err := kv.Load(k)
			assert.NoError(t, err)
			assert.Equal(t, v, actualV)
		}

		multiLoadTests := []struct {
			inputKeys      []string
			expectedValues []string
		}{
			{[]string{"key_1"}, []string{"value_1"}},
			{[]string{"key_1", "key_2", "key_3/a"}, []string{"value_1", "value_2", "value_3a"}},
			{[]string{"multikey_1", "multikey_2"}, []string{"multivalue_1", "multivalue_2"}},
			{[]string{"_"}, []string{"other"}},
		}

		for _, test := range multiLoadTests {
			vs, err := kv.MultiLoad(test.inputKeys)
			assert.NoError(t, err)
			assert.Equal(t, test.expectedValues, vs)
		}

		invalidMultiLoad := []struct {
			invalidKeys    []string
			expectedValues []string
		}{
			{[]string{"a", "key_1"}, []string{"", "value_1"}},
			{[]string{".....", "key_1"}, []string{"", "value_1"}},
			{[]string{"*********"}, []string{""}},
			{[]string{"key_1", "1"}, []string{"value_1", ""}},
		}

		for _, test := range invalidMultiLoad {
			vs, err := kv.MultiLoad(test.invalidKeys)
			assert.Error(t, err)
			assert.Equal(t, test.expectedValues, vs)
		}

		removeWithPrefixTests := []string{
			"key_1",
			"multi",
		}

		for _, k := range removeWithPrefixTests {
			err = kv.RemoveWithPrefix(k)
			assert.NoError(t, err)

			ks, vs, err := kv.LoadWithPrefix(k)
			assert.Empty(t, ks)
			assert.Empty(t, vs)
			assert.NoError(t, err)
		}

		multiRemoveTests := []string{
			"key_2",
			"key_3/a",
			"multikey_2",
			"_",
		}

		err = kv.MultiRemove(multiRemoveTests)
		assert.NoError(t, err)

		ks, vs, err := kv.LoadWithPrefix("")
		assert.NoError(t, err)
		assert.Empty(t, ks)
		assert.Empty(t, vs)

		multiSaveAndRemoveTests := []struct {
			multiSaves   map[string]string
			multiRemoves []string
		}{
			{map[string]string{"key_1": "value_1"}, []string{}},
			{map[string]string{"key_2": "value_2"}, []string{"key_1"}},
			{map[string]string{"key_3/a": "value_3a"}, []string{"key_2"}},
			{map[string]string{"multikey_1": "multivalue_1"}, []string{}},
			{map[string]string{"multikey_2": "multivalue_2"}, []string{"multikey_1", "key_3/a"}},
			{make(map[string]string), []string{"multikey_2"}},
		}
		for _, test := range multiSaveAndRemoveTests {
			err = kv.MultiSaveAndRemove(test.multiSaves, test.multiRemoves)
			assert.NoError(t, err)
		}

		ks, vs, err = kv.LoadWithPrefix("")
		assert.NoError(t, err)
		assert.Empty(t, ks)
		assert.Empty(t, vs)
	})

	te.Run("kv MultiSaveAndRemoveWithPrefix", func(t *testing.T) {
		rootPath := "/tikv/test/root/multi_remove_with_prefix"
		kv := NewTiKV(txnClient, rootPath)
		defer kv.Close()
		defer kv.RemoveWithPrefix("")

		prepareTests := map[string]string{
			"x/abc/1": "1",
			"x/abc/2": "2",
			"x/def/1": "10",
			"x/def/2": "20",
			"x/den/1": "100",
			"x/den/2": "200",
		}

		// MultiSaveAndRemoveWithPrefix
		err := kv.MultiSave(prepareTests)
		require.NoError(t, err)
		multiSaveAndRemoveWithPrefixTests := []struct {
			multiSave map[string]string
			prefix    []string

			loadPrefix         string
			lengthBeforeRemove int
			lengthAfterRemove  int
		}{
			{map[string]string{}, []string{"x/abc", "x/def", "x/den"}, "x", 6, 0},
			{map[string]string{"y/a": "vvv", "y/b": "vvv"}, []string{}, "y", 0, 2},
			{map[string]string{"y/c": "vvv"}, []string{}, "y", 2, 3},
			{map[string]string{"p/a": "vvv"}, []string{"y/a", "y"}, "y", 3, 0},
			{map[string]string{}, []string{"p"}, "p", 1, 0},
		}

		for _, test := range multiSaveAndRemoveWithPrefixTests {
			k, _, err := kv.LoadWithPrefix(test.loadPrefix)
			assert.NoError(t, err)
			assert.Equal(t, test.lengthBeforeRemove, len(k))

			err = kv.MultiSaveAndRemoveWithPrefix(test.multiSave, test.prefix)
			assert.NoError(t, err)

			k, _, err = kv.LoadWithPrefix(test.loadPrefix)
			assert.NoError(t, err)
			assert.Equal(t, test.lengthAfterRemove, len(k))
		}
	})

	te.Run("kv failed to start txn", func(t *testing.T) {
		rootPath := "/tikv/test/root/start_exn"
		kv := NewTiKV(txnClient, rootPath)
		defer kv.Close()

		beginTxn = func(txn *txnkv.Client) (*transaction.KVTxn, error) {
			return nil, fmt.Errorf("bad txn!")
		}
		defer func() {
			beginTxn = tiTxnBegin
		}()
		err := kv.Save("key1", "v1")
		assert.Error(t, err)
		err = kv.MultiSave(map[string]string{"A/100": "v1"})
		assert.Error(t, err)
		err = kv.Remove("key1")
		assert.Error(t, err)
		err = kv.MultiRemove([]string{"key_1", "key_2"})
		assert.Error(t, err)
		err = kv.MultiSaveAndRemove(map[string]string{"key_1": "value_1"}, []string{})
		assert.Error(t, err)
		err = kv.MultiSaveAndRemoveWithPrefix(map[string]string{"y/c": "vvv"}, []string{"/"})
		assert.Error(t, err)
	})

	te.Run("kv failed to commit txn", func(t *testing.T) {
		rootPath := "/tikv/test/root/commit_txn"
		kv := NewTiKV(txnClient, rootPath)
		defer kv.Close()

		commitTxn = func(txn *transaction.KVTxn, ctx context.Context) error {
			return fmt.Errorf("bad txn commit!")
		}
		defer func() {
			commitTxn = tiTxnCommit
		}()
		var err error
		err = kv.Save("key1", "v1")
		assert.Error(t, err)
		err = kv.MultiSave(map[string]string{"A/100": "v1"})
		assert.Error(t, err)
		err = kv.Remove("key1")
		assert.Error(t, err)
		err = kv.MultiRemove([]string{"key_1", "key_2"})
		assert.Error(t, err)
		err = kv.MultiSaveAndRemove(map[string]string{"key_1": "value_1"}, []string{})
		assert.Error(t, err)
		err = kv.MultiSaveAndRemoveWithPrefix(map[string]string{"y/c": "vvv"}, []string{"/"})
		assert.Error(t, err)
	})
}

func TestWalkWithPagination(t *testing.T) {
	rootPath := "/tikv/test/root/pagination"
	kv := NewTiKV(txnClient, rootPath)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	kvs := map[string]string{
		"A/100":    "v1",
		"AA/100":   "v2",
		"AB/100":   "v3",
		"AB/2/100": "v4",
		"B/100":    "v5",
	}

	err := kv.MultiSave(kvs)
	assert.NoError(t, err)
	for k, v := range kvs {
		actualV, err := kv.Load(k)
		assert.NoError(t, err)
		assert.Equal(t, v, actualV)
	}

	t.Run("apply function error ", func(t *testing.T) {
		err = kv.WalkWithPrefix("A", 5, func(key []byte, value []byte) error {
			return errors.New("error")
		})
		assert.Error(t, err)
	})

	t.Run("get with non-exist prefix ", func(t *testing.T) {
		err = kv.WalkWithPrefix("non-exist-prefix", 5, func(key []byte, value []byte) error {
			return nil
		})
		assert.NoError(t, err)
	})

	t.Run("with different pagination", func(t *testing.T) {
		testFn := func(pagination int) {
			expected := map[string]string{
				"A/100":    "v1",
				"AA/100":   "v2",
				"AB/100":   "v3",
				"AB/2/100": "v4",
			}

			expectedKeys := maps.Keys(expected)
			sort.Strings(expectedKeys)

			ret := make(map[string]string)
			actualKeys := make([]string, 0)

			err = kv.WalkWithPrefix("A", pagination, func(key []byte, value []byte) error {
				k := string(key)
				k = k[len(rootPath)+1:]
				ret[k] = string(value)
				actualKeys = append(actualKeys, k)
				return nil
			})

			assert.NoError(t, err)
			assert.Equal(t, expected, ret, fmt.Errorf("pagination: %d", pagination))
			// Ignore the order.
			assert.ElementsMatch(t, expectedKeys, actualKeys, fmt.Errorf("pagination: %d", pagination))
		}

		for p := -1; p < 6; p++ {
			testFn(p)
		}
		testFn(-100)
		testFn(100)
	})
}

func TestElapse(t *testing.T) {
	start := time.Now()
	isElapse := CheckElapseAndWarn(start, "err message")
	assert.Equal(t, isElapse, false)

	time.Sleep(2001 * time.Millisecond)
	isElapse = CheckElapseAndWarn(start, "err message")
	assert.Equal(t, isElapse, true)
}

func TestHas(t *testing.T) {
	rootPath := "/tikv/test/root/pagination"
	kv := NewTiKV(txnClient, rootPath)
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	has, err := kv.Has("key1")
	assert.NoError(t, err)
	assert.False(t, has)

	err = kv.Save("key1", "value1")
	assert.NoError(t, err)

	err = kv.Save("key1", EmptyValueString)
	assert.Error(t, err)

	has, err = kv.Has("key1")
	assert.NoError(t, err)
	assert.True(t, has)

	err = kv.Remove("key1")
	assert.NoError(t, err)

	has, err = kv.Has("key1")
	assert.NoError(t, err)
	assert.False(t, has)
}

func TestHasPrefix(t *testing.T) {
	rootPath := "/etcd/test/root/hasprefix"
	kv := NewTiKV(txnClient, rootPath)
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	has, err := kv.HasPrefix("key")
	assert.NoError(t, err)
	assert.False(t, has)

	err = kv.Save("key1", "value1")
	assert.NoError(t, err)

	has, err = kv.HasPrefix("key")
	assert.NoError(t, err)
	assert.True(t, has)

	err = kv.Remove("key1")
	assert.NoError(t, err)

	has, err = kv.HasPrefix("key")
	assert.NoError(t, err)
	assert.False(t, has)
}

func TestEmptyKey(t *testing.T) {
	rootPath := "/etcd/test/root/loadempty"
	kv := NewTiKV(txnClient, rootPath)
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	has, err := kv.HasPrefix("key")
	assert.NoError(t, err)
	assert.False(t, has)

	err = kv.Save("key", "")
	assert.NoError(t, err)

	has, err = kv.HasPrefix("key")
	assert.NoError(t, err)
	assert.True(t, has)

	val, err := kv.Load("key")
	assert.NoError(t, err)
	assert.Equal(t, val, "")

	_, vals, err := kv.LoadWithPrefix("key")
	assert.NoError(t, err)
	assert.Equal(t, vals[0], "")

	vals, err = kv.MultiLoad([]string{"key"})
	assert.NoError(t, err)
	assert.Equal(t, vals[0], "")

	var res string
	nothing := func(key, val []byte) error {
		res = string(val)
		return nil
	}

	err = kv.WalkWithPrefix("", 1, nothing)
	assert.NoError(t, err)
	assert.Equal(t, res, "")

	multiSaveTests := map[string]string{
		"key1": "",
	}
	err = kv.MultiSave(multiSaveTests)
	assert.NoError(t, err)
	val, err = kv.Load("key1")
	assert.NoError(t, err)
	assert.Equal(t, val, "")

	multiSaveTests = map[string]string{
		"key2": "",
	}
	err = kv.MultiSaveAndRemove(multiSaveTests, nil)
	assert.NoError(t, err)
	val, err = kv.Load("key2")
	assert.NoError(t, err)
	assert.Equal(t, val, "")

	multiSaveTests = map[string]string{
		"key3": "",
	}
	err = kv.MultiSaveAndRemoveWithPrefix(multiSaveTests, nil)
	assert.NoError(t, err)
	val, err = kv.Load("key3")
	assert.NoError(t, err)
	assert.Equal(t, val, "")
}

func TestScanSize(t *testing.T) {
	scan_size := SnapshotScanSize
	kv := NewTiKV(txnClient, "/")
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	// Test total > scansize
	key_map := map[string]string{}
	for i := 1; i <= scan_size+100; i++ {
		a := fmt.Sprintf("%v", i)
		key_map[a] = a
	}

	err = kv.MultiSave(key_map)
	assert.NoError(t, err)

	keys, _, err := kv.LoadWithPrefix("")
	assert.NoError(t, err)
	assert.Equal(t, len(keys), scan_size+100)

	err = kv.RemoveWithPrefix("")
	require.NoError(t, err)
}

func TestTiKVUnimplemented(t *testing.T) {
	kv := NewTiKV(txnClient, "/")
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	defer kv.Close()
	defer kv.RemoveWithPrefix("")

	_, err = kv.CompareVersionAndSwap("k", 1, "target")
	assert.Error(t, err)
}

func TestTxnWithPredicates(t *testing.T) {
	kv := NewTiKV(txnClient, "/")
	err := kv.RemoveWithPrefix("")
	require.NoError(t, err)

	prepareKV := map[string]string{
		"lease1": "1",
		"lease2": "2",
	}

	err = kv.MultiSave(prepareKV)
	require.NoError(t, err)

	multiSaveAndRemovePredTests := []struct {
		tag           string
		multiSave     map[string]string
		preds         []predicates.Predicate
		expectSuccess bool
	}{
		{"predicate_ok", map[string]string{"a": "b"}, []predicates.Predicate{predicates.ValueEqual("lease1", "1")}, true},
		{"predicate_fail", map[string]string{"a": "b"}, []predicates.Predicate{predicates.ValueEqual("lease1", "2")}, false},
	}

	for _, test := range multiSaveAndRemovePredTests {
		t.Run(test.tag, func(t *testing.T) {
			err := kv.MultiSaveAndRemove(test.multiSave, nil, test.preds...)
			t.Log(err)
			if test.expectSuccess {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
			err = kv.MultiSaveAndRemoveWithPrefix(test.multiSave, nil, test.preds...)
			if test.expectSuccess {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}
