// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package staterecord

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/kubernetes"
	corev1client "k8s.io/client-go/kubernetes/typed/core/v1"
)

// KubernetesStore is a [Store] backed by Secrets in one Kubernetes
// namespace, with metadata.resourceVersion as the version and no lock.
// GitHub issue #1392, under the epic #1398 ruling: a Kubernetes-only estate
// should not need an AWS account to keep its records anywhere but one
// operator's disk.
//
// # What is genuinely atomic
//
// Every conditional operation is one API request carrying its condition, and
// the API server enforces it:
//
//   - [KubernetesStore.PutIfAbsent] and a "" [KubernetesStore.PutIfVersion]
//     call send Create, which the API server refuses with 409 AlreadyExists
//     if an object of that name is there.
//   - A non-"" [KubernetesStore.PutIfVersion] call sends Update with
//     metadata.resourceVersion set to the version THE CALLER got from Get.
//     The API server refuses with 409 Conflict if the stored object has moved
//     on. The version is never re-read inside the call: the stock backend's
//     own Put does read-then-update (client.go:86) and that is exactly the
//     race window this interface exists to close.
//   - [KubernetesStore.Delete] sends Preconditions.ResourceVersion, the
//     API server's conditional delete.
//
// After a refusal this store issues one extra Get purely to name the version
// the store now holds in a [VersionConflictError]; that read is not part of
// the guarantee, which the refused request already made.
//
// # No lock, and no Lease
//
// The stock kubernetes backend takes a coordination.k8s.io Lease per
// workspace. Nothing here does. #1332 ruled the record store lock-free with a
// conditional write per record, and a Lease would put one lock back in front
// of an estate. A run killed mid-apply leaves no Lease to break, because it
// took none.
//
// # What this store does not manage
//
// The namespace. It is the read isolation boundary (see [KubernetesConfig.Namespace])
// and creating it is a cluster-admin act, not something a record write does
// on the way past. A namespace that is not there is refused by name, with the
// kubectl line that creates it.
//
// # What a listing is, and why it carries no label selector
//
// [KubernetesStore.List] and [KubernetesStore.GetAll] LIST the namespace with
// no selector and attribute each object client-side, by the key its own
// annotation carries and by its name. They used to select on
// app.kubernetes.io/managed-by and tofu-estate server-side, which is cheaper
// and which loses a record the moment either label goes: the object was then
// in no listing, with a nil error, while a Get of its key still served it, and
// an estate that reads as having fewer records than it has is planned against
// as if the missing ones were never created. GitHub issue #1448 measured it on
// kind. A selector cannot find an object by the label it is missing, so the
// narrowing and the refusal cannot both be had; the refusal is the one worth
// keeping ([UnlabelledRecordError]).
//
// What that costs is that a LIST carries every Secret in the namespace rather
// than this estate's records only. The default arrangement is one namespace
// per estate, where the difference is nothing; a namespace shared by
// configuration pays for the other estates' objects on the wire, and skips
// them client-side by their own tofu-estate label. No new permission is
// needed: anything that can list Secrets in the namespace could already read
// every record in it, which is decision 5 in this package's doc.
type KubernetesStore struct {
	secrets   corev1client.SecretInterface
	clientset kubernetes.Interface
	namespace string
	keyPrefix string
	estate    string

	// insecureTLS is [KubernetesConfig.InsecureTLS], kept for the contract
	// and read by nothing else.
	insecureTLS bool

	// listPageSize bounds one page of a LIST. Zero takes
	// [DefaultKubernetesListPageSize].
	listPageSize int64

	// namespaceUnaskable remembers that this identity may not get the
	// namespace, so [KubernetesStore.namespaceFault] asks once per run rather
	// than putting a refused call in front of every read that answers
	// "nothing here".
	namespaceUnaskable atomic.Bool

	// writeAuthz remembers what the API server's authorizer said about each
	// write verb and object name this run asked about, so a refused
	// write-back asks once rather than once per record. Keys are
	// verb + "\x00" + name, values are authzAnswer. See
	// [KubernetesStore.authorizerAllowsWrite].
	writeAuthz sync.Map
}

// KubernetesConfig configures a [KubernetesStore].
type KubernetesConfig struct {
	// Secrets is the namespaced Secret client every call goes through. The
	// caller builds and authenticates it - kubeconfig, in-cluster config, an
	// exec credential plugin - and this package has no opinion on any of
	// that, the same position [S3Config.Client] takes.
	Secrets corev1client.SecretInterface

	// Clientset is the same cluster connection, unscoped, and it is used for
	// exactly one thing: the cluster contract (kubernetescontract.go, GitHub
	// issue #1393), which asks the API server's own authorizer what this
	// identity may do and reads the estate boundary policy. No record ever
	// goes through it.
	//
	// Optional. Nil leaves [KubernetesStore.CheckContract] reporting
	// that it has no client, which is what a caller that built the store
	// from a bare SecretInterface gets - the conformance suite, for one.
	Clientset kubernetes.Interface

	// InsecureTLS says the caller built Secrets and Clientset not to verify
	// the API server's certificate, which is the record_store block's
	// `insecure = true`. The store does nothing differently for it. The
	// cluster contract reports it as a finding (GitHub issue #1448), and it
	// is carried here because neither client says how it was built.
	InsecureTLS bool

	// Namespace is the Kubernetes namespace Secrets writes into. It is
	// carried here for the error text, since a namespaced client does not
	// say which namespace it is bound to.
	//
	// It is the read isolation boundary. RBAC cannot condition on a label,
	// and admission never sees a get or a list, so anything that can read
	// Secrets in this namespace reads every record in it. One namespace per
	// estate's records is what keeps one estate out of another's.
	Namespace string

	// KeyPrefix is joined ahead of every key this store is asked for, the
	// same opaque string [S3Config.KeyPrefix] is. Empty means keys are used
	// as given, which is what internal/live/projection passes (see its
	// backendKeyPrefix).
	KeyPrefix string

	// Estate is the estate name every Secret this store writes carries as
	// its tofu-estate LABEL, so live/kubernetes/estate-boundary.yaml fences
	// writes to an estate's records with no new policy.
	//
	// Required, and refused at open when it cannot be a label value: an
	// estate name may be 128 characters and a label value caps at 63
	// (#1016, #1396). A store that silently dropped the label would write
	// records the boundary policy does not see.
	Estate string

	// ListPageSize bounds how many Secrets one LIST page returns. Zero takes
	// [DefaultKubernetesListPageSize].
	ListPageSize int64
}

// DefaultKubernetesListPageSize is how many Secrets one page of a LIST
// returns unless [KubernetesConfig.ListPageSize] says otherwise. A LIST here
// carries every object's whole payload, so the page bounds a response size
// and not just a count.
const DefaultKubernetesListPageSize = 200

const (
	// KubernetesSecretNamePrefix is the fixed, readable start of every Secret
	// name this store writes. What follows is the key's SHA-256, so an
	// operator reading `kubectl get secrets` can tell a record from anything
	// else in the namespace without knowing the hash.
	KubernetesSecretNamePrefix = "tofu-record-"

	// KubernetesRecordKeyAnnotation holds the record's full, unhashed key.
	// The name is a hash, so this is the only place the key survives, and
	// [KubernetesStore.List] reads the keys it returns out of it.
	KubernetesRecordKeyAnnotation = "choudoufu.intentius.io/record-key"

	// KubernetesNamespaceLabel is the key's first "/"-delimited segment -
	// "tofu-records", "tofu-hints", "tofu-outputs" - or
	// [KubernetesNamespaceLabelOther] when that segment cannot be a label
	// value. It is never what a returned key is read from.
	//
	// Nothing in this store selects on it any more. It narrowed a LIST
	// server-side until #1448 took the selector off the listing entirely (see
	// [KubernetesStore]), and it is still written because an operator reading
	// or sorting a namespace by hand has nothing else to group records by:
	// the name is a hash and the key is an annotation.
	KubernetesNamespaceLabel = "choudoufu.intentius.io/record-namespace"

	// KubernetesNamespaceLabelOther is [KubernetesNamespaceLabel]'s value for
	// a key whose first segment is not a valid label value. Every Secret
	// carries the label with some value, so the label is never absent on an
	// object this store wrote.
	KubernetesNamespaceLabelOther = "other"

	// KubernetesManagedByLabel and KubernetesManagedByValue mark the Secrets
	// this store owns. A listing reads them to tell one estate's records from
	// another's in a shared namespace, and a record that is missing them is
	// refused by name rather than skipped ([UnlabelledRecordError]).
	KubernetesManagedByLabel = "app.kubernetes.io/managed-by"
	KubernetesManagedByValue = "choudoufu"

	// KubernetesEstateLabel is markers.TagEstate. It is spelled out rather
	// than imported: this package holds no choudoufu concepts (see doc.go),
	// and internal/live/markers is one. internal/live/projection's
	// kubernetes_store_test.go pins the two spellings equal, so the store's
	// Secrets cannot stop carrying the label estate-boundary.yaml fences on.
	KubernetesEstateLabel = "tofu-estate"

	// kubernetesPayloadKey is the Secret data key the gzipped payload lives
	// under, and kubernetesEncodingAnnotation says how, both spelled the way
	// the stock backend spells them (internal/backend/remote-state/kubernetes)
	// so an operator reading either object reads the same shape.
	kubernetesPayloadKey         = "tfstate"
	kubernetesEncodingAnnotation = "encoding"
	kubernetesEncodingValue      = "gzip"
)

// MaxKubernetesRecordBytes is the largest compressed payload this store will
// write. It is corev1.MaxSecretSize: the API server sums a Secret's data
// values and refuses the object above one MiB.
//
// The refusal happens HERE, before the request, and on the COMPRESSED length,
// because that is the number the API server will measure. A record refused by
// the server arrives as a generic Invalid and is indistinguishable at a glance
// from a dozen other validation failures; refused by name, an operator is told
// which record and how far over.
//
// The rest of the object is not counted against this. Labels and annotations
// live outside data, and the whole of what this store puts there - the key, an
// address, three labels - is under two kilobytes. The serialized object is
// about 4/3 of the payload once the API server base64s the data, so a record
// at this limit is roughly 1.4 MiB on the wire, inside etcd's own 1.5 MiB
// request limit with nothing to spare. A store that wanted to use the last
// hundred kilobytes would be trading a named refusal for an etcd one.
const MaxKubernetesRecordBytes = corev1.MaxSecretSize

// RecordTooLargeError reports a record this store will not write because the
// backing object cannot hold it. Named so a caller can tell it from a denial
// or an outage without parsing prose.
type RecordTooLargeError struct {
	Key   string
	Bytes int
	Limit int
}

func (e *RecordTooLargeError) Error() string {
	return fmt.Sprintf(
		"staterecord: kubernetes: the record at %q is %d bytes after compression and a Secret holds at most %d, so this record cannot be written to a cluster; a record that large is a resource whose whole value is being recorded, and either the resource belongs in a store with no such limit (record_store \"s3\") or the value does not belong in a record",
		e.Key, e.Bytes, e.Limit)
}

// NamespaceMissingError reports that the Kubernetes namespace this store
// writes into does not exist. It carries the kubectl line that creates it.
//
// This is not a missing record. A LIST in a namespace that is not there comes
// back EMPTY rather than failing, and an empty listing reads as an empty
// estate - #688's failure whole, arriving through a different door. The
// namespace is not created on the way past, because it is the read isolation
// boundary and who may create one is a cluster-admin decision.
type NamespaceMissingError struct {
	Namespace string
	Err       error
}

func (e *NamespaceMissingError) Error() string {
	return fmt.Sprintf(
		"staterecord: kubernetes: namespace %q does not exist, so there is nowhere in this cluster for this estate's records; create it with `kubectl create namespace %s` and grant this run's identity get, list, create, update and delete on secrets in it: %v",
		e.Namespace, e.Namespace, e.Err)
}

func (e *NamespaceMissingError) Unwrap() error { return e.Err }

// NamespaceTerminatingError reports that the Kubernetes namespace this store
// writes into is being deleted. It is [NamespaceMissingError] a few seconds
// early: the API server is removing every object in the namespace, so a LIST
// of it answers 200 with an empty list as soon as the records are gone, and
// that empty listing reads as an estate with no records.
//
// It is kept apart from [NamespaceMissingError] because the operator's next
// move is different. A missing namespace is created; a terminating one has to
// finish going away before it can be.
type NamespaceTerminatingError struct {
	Namespace string
	Err       error
}

func (e *NamespaceTerminatingError) Error() string {
	s := fmt.Sprintf(
		"staterecord: kubernetes: namespace %q is being deleted, so this cluster is removing every record this estate has and a listing of it is not an empty estate; wait for the delete to finish (`kubectl get namespace %s` stops answering), create it again with `kubectl create namespace %s` and grant this run's identity get, list, create, update and delete on secrets in it, and expect to import or re-record anything the delete took with it",
		e.Namespace, e.Namespace, e.Namespace)
	if e.Err != nil {
		s += ": " + e.Err.Error()
	}
	return s
}

func (e *NamespaceTerminatingError) Unwrap() error { return e.Err }

// UnlabelledRecordError reports a Secret that is a record object under this
// store's own keys - it is named the way this store names a record, and its
// annotation carries a key under the prefix being listed - and that does not
// carry the labels every record this store writes carries.
//
// It is a refusal rather than an omission because of what the omission cost.
// The listing used to be a label selector, so a record whose tofu-estate or
// app.kubernetes.io/managed-by label was stripped - `kubectl label secret
// NAME tofu-estate-`, a restore that dropped labels, an admission policy
// mutating an object on the way past - was in neither List nor GetAll, with a
// nil error, while a Get of its key still served it. An estate then reads as
// having fewer records than it has, and the next plan proposes creating those
// live resources a second time. GitHub issue #1448.
//
// The labels are not put back by this store. A write that repaired them would
// be a write to an object the estate boundary policy does not currently fence,
// which is the one write that must not happen silently.
type UnlabelledRecordError struct {
	Namespace  string
	SecretName string
	Key        string

	// Missing is what the Secret has to carry, as "label=value", for each
	// label that is absent or holds another value.
	Missing []string
}

func (e *UnlabelledRecordError) Error() string {
	var labels []string
	for _, m := range e.Missing {
		name, _, _ := strings.Cut(m, "=")
		labels = append(labels, name)
	}
	return fmt.Sprintf(
		"staterecord: kubernetes: Secret %q in namespace %q holds the record for key %q and does not carry %s, so it is a record of this estate that no label selector can find; put the labels back with `kubectl -n %s label secret %s --overwrite %s`, or, if that object is another estate's record, label it with that estate's name and this listing will skip it",
		e.SecretName, e.Namespace, e.Key, strings.Join(labels, " and "),
		e.Namespace, e.SecretName, strings.Join(e.Missing, " "))
}

// MisnamedRecordError reports a Secret whose record-key annotation says it
// holds one key while its NAME is not the name that key hashes to.
//
// The name is how a read finds a record, so such an object is in every listing
// and reachable by nothing: Get of the key it claims says no record is there,
// and a PutIfVersion or a Delete carrying the version the listing gave
// conflicts forever. It is what a renamed record Secret looks like - a copy
// taken by hand, a restore under a new name.
type MisnamedRecordError struct {
	Namespace  string
	SecretName string
	Key        string
	WantName   string
}

func (e *MisnamedRecordError) Error() string {
	return fmt.Sprintf(
		"staterecord: kubernetes: Secret %q in namespace %q claims the record for key %q in its %s annotation, and that key hashes to Secret %q; the name is what a read looks up, so this object is in every listing and no read, write or delete of that key can reach it. If it is a copy, delete it with `kubectl -n %s delete secret %s`. If it is the record, live/STORAGE.md has the command that moves it to the name its key hashes to.",
		e.SecretName, e.Namespace, e.Key, KubernetesRecordKeyAnnotation, e.WantName,
		e.Namespace, e.SecretName)
}

// DuplicateRecordKeyError reports two or more Secrets in the namespace whose
// record-key annotation carries the SAME key.
//
// A listing used to return that key once per object and a bulk read kept
// whichever the cluster listed last, so which payload the run used was decided
// by list order. Only one of them can be named for the key - a name is a hash
// of it and a namespace holds one object per name - so the rest are copies,
// and the refusal says which is which rather than picking one.
type DuplicateRecordKeyError struct {
	Namespace   string
	Key         string
	SecretNames []string

	// WantName is the name Key hashes to: the one of SecretNames that is the
	// record, when it is among them at all.
	WantName string
}

func (e *DuplicateRecordKeyError) Error() string {
	var copies []string
	for _, name := range e.SecretNames {
		if name != e.WantName {
			copies = append(copies, name)
		}
	}
	s := fmt.Sprintf(
		"staterecord: kubernetes: Secrets %s in namespace %q all hold the record for key %q, so a listing returns that key once per object and a bulk read keeps whichever the cluster listed last; the key hashes to Secret %q, which is the record",
		strings.Join(e.SecretNames, ", "), e.Namespace, e.Key, e.WantName)
	if len(copies) > 0 {
		s += fmt.Sprintf(". Delete the copies with `kubectl -n %s delete secret %s`, once you have checked that none of them holds a payload the record does not",
			e.Namespace, strings.Join(copies, " "))
	}
	return s
}

// KeyCollisionError reports that the Secret a key hashes to holds a DIFFERENT
// key. A SHA-256 collision is not something to plan for and is something to
// refuse rather than serve: the alternative is one record silently answering
// for another.
//
// It is also what an operator gets for hand-editing a record Secret's key
// annotation, which is the case that will actually happen.
type KeyCollisionError struct {
	Key        string
	SecretName string
	FoundKey   string
}

func (e *KeyCollisionError) Error() string {
	return fmt.Sprintf(
		"staterecord: kubernetes: Secret %q holds the record for key %q, not %q; the name is a hash of the key and the key itself is in the %s annotation, so this is either a hash collision or an object whose annotation was edited by hand, and either way answering with the wrong record is worse than refusing",
		e.SecretName, e.FoundKey, e.Key, KubernetesRecordKeyAnnotation)
}

// NewKubernetesStore builds a [KubernetesStore] from cfg.
//
// The estate name is checked here, once, rather than at the first write. An
// estate name may be 128 characters (#1396) and a Kubernetes label value caps
// at 63, so there are estate names this store cannot fence. Refusing at open
// means the operator finds out before a plan is built, and not after half the
// records were written unlabelled.
func NewKubernetesStore(cfg KubernetesConfig) (*KubernetesStore, error) {
	if cfg.Secrets == nil {
		return nil, fmt.Errorf("staterecord: kubernetes: Secrets client must not be nil")
	}
	if cfg.Namespace == "" {
		return nil, fmt.Errorf("staterecord: kubernetes: Namespace must not be empty")
	}
	if cfg.Estate == "" {
		return nil, fmt.Errorf("staterecord: kubernetes: Estate must not be empty: every Secret this store writes carries it as the %s label, which is what live/kubernetes/estate-boundary.yaml fences writes with", KubernetesEstateLabel)
	}
	if errs := validation.IsValidLabelValue(cfg.Estate); len(errs) > 0 {
		return nil, fmt.Errorf(
			"staterecord: kubernetes: estate name %q (%d characters) cannot be a Kubernetes label value, so no Secret this store wrote would carry the %s label and live/kubernetes/estate-boundary.yaml would fence none of its records: %s. Rename the estate, or keep its records in a store with no such limit (record_store \"s3\")",
			cfg.Estate, len(cfg.Estate), KubernetesEstateLabel, strings.Join(errs, "; "))
	}
	return &KubernetesStore{
		secrets:      cfg.Secrets,
		clientset:    cfg.Clientset,
		namespace:    cfg.Namespace,
		keyPrefix:    cfg.KeyPrefix,
		estate:       cfg.Estate,
		insecureTLS:  cfg.InsecureTLS,
		listPageSize: cfg.ListPageSize,
	}, nil
}

// storeKey joins s.keyPrefix and key, the same way [S3Store] builds an object
// key, so a prefix set on the store scopes its keyspace.
func (s *KubernetesStore) storeKey(key string) string {
	if s.keyPrefix == "" {
		return key
	}
	return strings.TrimSuffix(s.keyPrefix, "/") + "/" + key
}

func (s *KubernetesStore) keyFromStoreKey(storeKey string) (string, bool) {
	if s.keyPrefix == "" {
		return storeKey, true
	}
	full := strings.TrimSuffix(s.keyPrefix, "/") + "/"
	if !strings.HasPrefix(storeKey, full) {
		return "", false
	}
	return strings.TrimPrefix(storeKey, full), true
}

// SecretName is the Secret this store reads and writes key at: a fixed,
// readable prefix and the SHA-256 of the store key.
//
// A record key is not a DNS-1123 name and cannot be made into one without
// losing information: keys carry "/" and base64url runs, they are
// case-sensitive where a name is not, and projection's own encoder already
// produces keys past a name's 253-character limit (the conformance suite's
// chunked key is 500). A hash is fixed-length, case-free and total. What it
// costs is that a name no longer says which record it is, which the key
// annotation pays back, and that two keys could in principle collide, which
// [KeyCollisionError] refuses rather than serves.
//
// Exported so an operator can be told the object to look at - the same reason
// [S3Store.ObjectKey] is exported (#916).
func (s *KubernetesStore) SecretName(key string) string {
	sum := sha256.Sum256([]byte(s.storeKey(key)))
	return KubernetesSecretNamePrefix + hex.EncodeToString(sum[:])
}

// namespaceLabelValue is the key's first "/"-delimited segment when that can
// be a label value, and [KubernetesNamespaceLabelOther] otherwise. Every
// Secret carries the label, so a LIST selecting on it never misses an object
// because the label was absent.
func namespaceLabelValue(storeKey string) string {
	seg, _, _ := strings.Cut(storeKey, "/")
	if seg == "" || len(validation.IsValidLabelValue(seg)) > 0 {
		return KubernetesNamespaceLabelOther
	}
	return seg
}

// notFoundIsNamespace reports whether a NotFound was the NAMESPACE's and not
// the Secret's. The API server names what it could not find in
// Status.Details.Kind, which is the only thing that tells the two apart, and
// reading a missing namespace as a missing record is this backend's version of
// #1383's missing bucket.
//
// It is only ever true of a CREATE. Measured on kind (#1448): a GET of a
// Secret in a namespace that does not exist is `404 secrets "x" not found`,
// with Kind "secrets", and a LIST of one is `200 {"items":[]}`. So this leg
// answers the write path and [KubernetesStore.namespaceFault] answers the
// read path; neither replaces the other.
func notFoundIsNamespace(err error) bool {
	var status k8serrors.APIStatus
	if !errors.As(err, &status) {
		return false
	}
	details := status.Status().Details
	return details != nil && details.Kind == "namespaces"
}

// namespaceFault is how a read that answered "nothing here" finds out that
// the nothing was the namespace. It returns the refusal to raise instead of
// that answer, and nil when the namespace is there and usable.
//
// A read cannot tell the two apart by what it is told - see
// [notFoundIsNamespace] - so this asks the one question that can: the
// namespace itself. A namespace being DELETED counts as gone, because the
// cluster is removing every record in it and the listing empties out while it
// happens ([NamespaceTerminatingError]).
//
// # When it cannot ask, and what covers that
//
// It answers nil. A store built from a bare SecretInterface has no clientset
// (the conformance suite, for one), and the Role this fork recommends holds
// no cluster-scoped get on namespaces, which checkNamespaceAccess in
// kubernetescontract.go already reports rather than asserts. Under either, an
// empty listing stays an empty listing here.
//
// What covers it is one layer up. internal/live/projection writes a
// provisioning sentinel into every store it opens, so a listing that does not
// carry the sentinel is not an empty estate whatever this identity may ask
// about namespaces - a permission-free signal, and the reason a missing
// namespace is refused on the open path even under a scoped identity. That
// rule lives there because this package holds no notion of a sentinel (see
// this package's doc comment).
func (s *KubernetesStore) namespaceFault(ctx context.Context) error {
	if s.clientset == nil || s.namespaceUnaskable.Load() {
		return nil
	}
	ns, err := s.clientset.CoreV1().Namespaces().Get(ctx, s.namespace, metav1.GetOptions{})
	switch {
	case err == nil:
	case k8serrors.IsNotFound(err):
		return &NamespaceMissingError{Namespace: s.namespace, Err: err}
	case k8serrors.IsForbidden(err):
		// Asked once per store. A permission does not change inside a run,
		// and repeating the question would put one refused call in front of
		// every read that answers "nothing here".
		s.namespaceUnaskable.Store(true)
		return nil
	default:
		return fmt.Errorf("staterecord: kubernetes: a read of namespace %q answered that nothing is there, an absent namespace answers a read the same way, and the question that tells the two apart failed: %w", s.namespace, err)
	}
	if ns.Status.Phase == corev1.NamespaceTerminating || ns.DeletionTimestamp != nil {
		return &NamespaceTerminatingError{Namespace: s.namespace}
	}
	return nil
}

// classify turns a client-go error into this store's own. It is the one place
// a Kubernetes failure becomes a staterecord one.
func (s *KubernetesStore) classify(doing, key string, err error) error {
	if notFoundIsNamespace(err) {
		return &NamespaceMissingError{Namespace: s.namespace, Err: err}
	}
	// A write into a namespace that is being deleted is refused with 403 and
	// a NamespaceTerminating cause (measured on kind). It is a 403 that has
	// nothing to do with this run's identity, and without this leg
	// [IsAccessDenied] reads it as one and #1370's reader tolerance carries
	// the run past a namespace whose records are being deleted underneath it.
	if k8serrors.HasStatusCause(err, corev1.NamespaceTerminatingCause) {
		return &NamespaceTerminatingError{Namespace: s.namespace, Err: err}
	}
	return fmt.Errorf("staterecord: kubernetes: %s %q in namespace %q: %w", doing, key, s.namespace, err)
}

// Get implements [Store].
func (s *KubernetesStore) Get(ctx context.Context, key string) ([]byte, string, bool, error) {
	if err := validateKey(key); err != nil {
		return nil, "", false, err
	}
	name := s.SecretName(key)
	secret, err := s.secrets.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if notFoundIsNamespace(err) {
			return nil, "", false, &NamespaceMissingError{Namespace: s.namespace, Err: err}
		}
		if k8serrors.IsNotFound(err) {
			// This Secret is not there. Whether the NAMESPACE is is a
			// different question, and a 404 naming the secret is the same
			// answer either way, so it is asked rather than assumed.
			if nsErr := s.namespaceFault(ctx); nsErr != nil {
				return nil, "", false, nsErr
			}
			return nil, "", false, nil
		}
		return nil, "", false, s.classify("getting", key, err)
	}
	payload, err := s.readSecret(key, secret)
	if err != nil {
		return nil, "", false, err
	}
	return payload, secret.ResourceVersion, true, nil
}

// readSecret checks that secret really holds key's record and decompresses it.
func (s *KubernetesStore) readSecret(key string, secret *corev1.Secret) ([]byte, error) {
	if got := secret.Annotations[KubernetesRecordKeyAnnotation]; got != s.storeKey(key) {
		return nil, &KeyCollisionError{Key: s.storeKey(key), SecretName: secret.Name, FoundKey: got}
	}
	payload, err := uncompressRecord(secret.Data[kubernetesPayloadKey])
	if err != nil {
		return nil, fmt.Errorf("staterecord: kubernetes: reading %q from Secret %q: %w", key, secret.Name, err)
	}
	return payload, nil
}

// buildSecret is the object a write sends. Every write carries the whole set
// of labels and annotations, so a record this store writes is never missing
// one.
//
// That is as far as the rule goes here, and it is where this store parts from
// [S3Store]'s tagging. An S3 object whose tags were stripped is still in every
// listing - a listing is by key prefix - so the next write to it puts the tags
// back, and the comment here used to say the same of a Secret. It was false:
// the listing was a label selector, so a Secret that lost a label was in no
// listing, nothing proposed a write to it, and nothing ever corrected it. A
// record that is not labelled like one is refused by name instead
// ([UnlabelledRecordError], GitHub issue #1448), and repairing it is an
// operator's kubectl line rather than a write this store makes to an object
// the estate boundary policy does not currently fence.
//
// tofu-estate is a LABEL because that is what live/kubernetes/estate-boundary.yaml
// reads and what a selector can match. The address goes in an ANNOTATION
// because a label value caps at 63 characters and a resource address does not
// (#1016). The context tags [WithObjectTags] carries are written the same way
// #1337 writes them on S3 objects, with the store's own estate winning on the
// estate key so an object can never name an estate other than the one the
// store was opened for.
func (s *KubernetesStore) buildSecret(ctx context.Context, key string, payload []byte) *corev1.Secret {
	storeKey := s.storeKey(key)
	annotations := map[string]string{
		KubernetesRecordKeyAnnotation: storeKey,
		kubernetesEncodingAnnotation:  kubernetesEncodingValue,
	}
	for k, v := range ObjectTags(ctx) {
		if k == KubernetesEstateLabel {
			continue
		}
		annotations[k] = v
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      s.SecretName(key),
			Namespace: s.namespace,
			Labels: map[string]string{
				KubernetesManagedByLabel: KubernetesManagedByValue,
				KubernetesEstateLabel:    s.estate,
				KubernetesNamespaceLabel: namespaceLabelValue(storeKey),
			},
			Annotations: annotations,
		},
		Data: map[string][]byte{kubernetesPayloadKey: payload},
	}
}

// conflictError builds the *[VersionConflictError] for a refused conditional
// write: one read, purely to name the version the store now holds. The write
// already failed atomically before this call is made. Mirrors
// [S3Store.conflictError], including what happens when the re-read fails too.
func (s *KubernetesStore) conflictError(ctx context.Context, key, expectedVersion string, cause error) error {
	_, actual, exists, err := s.Get(ctx, key)
	if err != nil {
		if cause == nil {
			return fmt.Errorf("staterecord: kubernetes: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w", key, err)
		}
		return fmt.Errorf("staterecord: kubernetes: %q failed its version condition, and the re-read that would name the version the store now holds failed too: %w (the condition failure: %w)", key, err, cause)
	}
	av := ""
	if exists {
		av = actual
	}
	return &VersionConflictError{Key: key, ExpectedVersion: expectedVersion, ActualVersion: av}
}

// PutIfAbsent implements [Store].
func (s *KubernetesStore) PutIfAbsent(ctx context.Context, key string, payload []byte) (string, error) {
	return s.PutIfVersion(ctx, key, payload, "")
}

// PutIfVersion implements [Store]. expectedVersion == "" is a Create; any
// other value is an Update carrying that resourceVersion.
//
// The version sent is the one the CALLER got from Get. Nothing is read inside
// this call to fill it in, which is what makes this a compare-and-swap rather
// than the stock backend's read-then-update.
func (s *KubernetesStore) PutIfVersion(ctx context.Context, key string, payload []byte, expectedVersion string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	compressed, err := compressRecord(payload)
	if err != nil {
		return "", fmt.Errorf("staterecord: kubernetes: compressing %q: %w", key, err)
	}
	// Before the request, and on the compressed length: see
	// [MaxKubernetesRecordBytes].
	if len(compressed) > MaxKubernetesRecordBytes {
		return "", &RecordTooLargeError{Key: s.storeKey(key), Bytes: len(compressed), Limit: MaxKubernetesRecordBytes}
	}
	secret := s.buildSecret(ctx, key, compressed)

	if expectedVersion == "" {
		out, createErr := s.secrets.Create(ctx, secret, metav1.CreateOptions{})
		if createErr != nil {
			if k8serrors.IsAlreadyExists(createErr) {
				return "", s.conflictError(ctx, key, "", createErr)
			}
			return "", s.classifyWrite(ctx, "creating", admissionVerbCreate, key, createErr)
		}
		return out.ResourceVersion, nil
	}

	secret.ResourceVersion = expectedVersion
	out, updateErr := s.secrets.Update(ctx, secret, metav1.UpdateOptions{})
	if updateErr != nil {
		if k8serrors.IsConflict(updateErr) {
			return "", s.conflictError(ctx, key, expectedVersion, updateErr)
		}
		// The update whose record is GONE. The API server answers an Update
		// carrying a resourceVersion for an object that no longer exists with
		// 404, not 409 - the same shape #1344 measured on real S3, where a
		// conditional PutObject for a deleted key answers NoSuchKey. It is the
		// same conflict from the caller's side: the version it read is not the
		// version the store holds, because the store holds none.
		//
		// Only for an update, and only for a 404 that named the SECRET. A
		// create has no version to conflict with, and a 404 that named the
		// NAMESPACE is not about this record at all.
		if k8serrors.IsNotFound(updateErr) && !notFoundIsNamespace(updateErr) {
			return "", s.conflictError(ctx, key, expectedVersion, updateErr)
		}
		return "", s.classifyWrite(ctx, "writing", admissionVerbUpdate, key, updateErr)
	}
	return out.ResourceVersion, nil
}

// Delete implements [Store]. expectedVersion == "" against an absent key is a
// no-op, checked with a Get first because a delete precondition has no "only
// if absent" form; any other value rides as Preconditions.ResourceVersion on
// the Delete itself.
func (s *KubernetesStore) Delete(ctx context.Context, key string, expectedVersion string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if expectedVersion == "" {
		_, _, exists, err := s.Get(ctx, key)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		return s.conflictError(ctx, key, expectedVersion, nil)
	}
	err := s.secrets.Delete(ctx, s.SecretName(key), metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{ResourceVersion: &expectedVersion},
	})
	if err != nil {
		if k8serrors.IsConflict(err) {
			return s.conflictError(ctx, key, expectedVersion, err)
		}
		if k8serrors.IsNotFound(err) && !notFoundIsNamespace(err) {
			// The record the caller read is gone: the version it holds is not
			// the store's. Same case as the update above.
			return s.conflictError(ctx, key, expectedVersion, err)
		}
		return s.classifyWrite(ctx, "deleting", admissionVerbDelete, key, err)
	}
	return nil
}

// List implements [Store]. One paginated LIST of the whole namespace, then an
// ordinary string-prefix filter on the key each Secret's annotation carries.
//
// The filter is client-side because a label selector cannot express a prefix
// and the key is not in the name. So is the rest of the attribution, and that
// is a deliberate trade rather than an oversight: see [KubernetesStore] for
// what a server-side selector lost, and [KubernetesStore.attributeRecord] for
// what stands in its place.
func (s *KubernetesStore) List(ctx context.Context, keyPrefix string) ([]string, error) {
	keys, _, err := s.list(ctx, keyPrefix, false)
	return keys, err
}

// listedRecord is one Secret the listing attributed to this store, kept until
// the whole namespace has been read so that the checks below see every object
// rather than the page one happens to be on.
type listedRecord struct {
	key     string
	name    string
	payload []byte
	version string
}

// list is the shared body of List and GetAll. withPayload decides whether the
// records are decompressed, since a LIST carries every object's data whether
// or not the caller wants it.
//
// Nothing is returned alongside a refusal. A short listing with a nil error is
// the failure this whole file is about, and a listing beside an error would be
// one a caller could use by mistake.
func (s *KubernetesStore) list(ctx context.Context, keyPrefix string, withPayload bool) ([]string, map[string]Record, error) {
	if err := validateKeyPrefix(keyPrefix); err != nil {
		return nil, nil, err
	}
	prefix := s.storeKey(keyPrefix)
	pageSize := s.listPageSize
	if pageSize < 1 {
		pageSize = DefaultKubernetesListPageSize
	}

	var found []listedRecord
	var unlabelled []error
	cont := ""
	for {
		page, err := s.secrets.List(ctx, metav1.ListOptions{
			Limit:    pageSize,
			Continue: cont,
		})
		if err != nil {
			if notFoundIsNamespace(err) {
				return nil, nil, &NamespaceMissingError{Namespace: s.namespace, Err: err}
			}
			return nil, nil, s.classify("listing", keyPrefix, err)
		}
		for i := range page.Items {
			secret := &page.Items[i]
			key, mine, err := s.attributeRecord(secret, prefix)
			if err != nil {
				unlabelled = append(unlabelled, err)
				continue
			}
			if !mine {
				continue
			}
			rec := listedRecord{key: key, name: secret.Name, version: secret.ResourceVersion}
			if withPayload {
				payload, err := uncompressRecord(secret.Data[kubernetesPayloadKey])
				if err != nil {
					return nil, nil, fmt.Errorf("staterecord: kubernetes: reading everything under %q: %q from Secret %q: %w", keyPrefix, key, secret.Name, err)
				}
				rec.payload = payload
			}
			found = append(found, rec)
		}
		cont = page.Continue
		if cont == "" {
			break
		}
	}

	// An empty listing and a full one say the same thing about the namespace,
	// which is nothing: a LIST in a namespace that is gone or going answers
	// 200 with no items.
	if err := s.namespaceFault(ctx); err != nil {
		return nil, nil, err
	}
	if err := s.listFault(found, unlabelled); err != nil {
		return nil, nil, err
	}

	keys := make([]string, 0, len(found))
	var records map[string]Record
	if withPayload {
		records = make(map[string]Record, len(found))
	}
	for _, rec := range found {
		keys = append(keys, rec.key)
		if withPayload {
			records[rec.key] = Record{Payload: rec.payload, Version: rec.version}
		}
	}
	sort.Strings(keys)
	return keys, records, nil
}

// attributeRecord decides what one Secret in the namespace is to this store:
// one of its records, another estate's, something that is not a record at
// all, or a record of this estate that is not labelled like one.
//
// It is the client-side half of listing with no label selector. What makes an
// object a record here is that it agrees with itself: its annotation carries a
// key, and its NAME is the name that key hashes to under this store's prefix.
// Labels are read to tell one estate's records from another's, which is what
// keeps a shared namespace working, and a record that carries none of them is
// refused rather than skipped.
//
// # What it cannot find
//
// A Secret that lost its labels AND was renamed. Neither signal is left, and
// claiming an object on the key annotation alone would let anything in the
// namespace that carries that annotation refuse this estate's runs. A renamed
// record that kept its labels is found and refused ([MisnamedRecordError]);
// an unlabelled one that kept its name is too; one that lost both is a Secret
// this store has no way to tell from someone else's.
func (s *KubernetesStore) attributeRecord(secret *corev1.Secret, prefix string) (key string, mine bool, err error) {
	storeKey, ok := secret.Annotations[KubernetesRecordKeyAnnotation]
	if !ok {
		// No key annotation: something else wrote it, or an operator edited
		// it. It is not a record, and inventing a key for it would put a key
		// in a listing that no Get can answer.
		return "", false, nil
	}
	if !strings.HasPrefix(storeKey, prefix) {
		return "", false, nil
	}
	key, ok = s.keyFromStoreKey(storeKey)
	if !ok {
		return "", false, nil
	}

	managedBy := secret.Labels[KubernetesManagedByLabel]
	estate := secret.Labels[KubernetesEstateLabel]
	if managedBy == KubernetesManagedByValue {
		switch estate {
		case s.estate:
			return key, true, nil
		case "":
			// Labelled as a record and not as anyone's: below.
		default:
			// Another estate's record, labelled the way this store labels its
			// own. A namespace may be shared by configuration, and one
			// estate's listing has never carried another's.
			return "", false, nil
		}
	}
	if !strings.HasPrefix(secret.Name, KubernetesSecretNamePrefix) {
		return "", false, nil
	}
	var missing []string
	if managedBy != KubernetesManagedByValue {
		missing = append(missing, KubernetesManagedByLabel+"="+KubernetesManagedByValue)
	}
	if estate != s.estate {
		missing = append(missing, KubernetesEstateLabel+"="+s.estate)
	}
	return "", false, &UnlabelledRecordError{
		Namespace:  s.namespace,
		SecretName: secret.Name,
		Key:        storeKey,
		Missing:    missing,
	}
}

// listFault is what a listing refuses with when the namespace holds a record
// object that no read of the keys it returns could answer for.
//
// More than one of these can be true of one namespace. The refusal names one
// and the next run names the next, and the order is the most specific first: a
// duplicate names both objects and says which of them the key hashes to, which
// is everything the misnamed refusal for the copy would have said and the name
// of the record besides.
func (s *KubernetesStore) listFault(found []listedRecord, unlabelled []error) error {
	byKey := map[string][]string{}
	for _, rec := range found {
		byKey[rec.key] = append(byKey[rec.key], rec.name)
	}
	for _, rec := range found {
		names := byKey[rec.key]
		if len(names) < 2 {
			continue
		}
		sorted := append([]string(nil), names...)
		sort.Strings(sorted)
		return &DuplicateRecordKeyError{
			Namespace:   s.namespace,
			Key:         s.storeKey(rec.key),
			SecretNames: sorted,
			WantName:    s.SecretName(rec.key),
		}
	}
	for _, rec := range found {
		if want := s.SecretName(rec.key); rec.name != want {
			return &MisnamedRecordError{
				Namespace:  s.namespace,
				SecretName: rec.name,
				Key:        s.storeKey(rec.key),
				WantName:   want,
			}
		}
	}
	if len(unlabelled) > 0 {
		return unlabelled[0]
	}
	return nil
}

// GetAll implements [BulkReader] in ONE paginated LIST.
//
// Kubernetes is the backend that genuinely can bulk-fetch: a LIST returns each
// Secret's data alongside its metadata, where S3's ListObjectsV2 returns keys
// and ETags and never a body. So there is no fan-out here and nothing to bound
// - [S3Store.GetAll]'s whole parallelism apparatus, and the completeness
// hazard that comes with it, has no counterpart.
//
// Complete or fail is still the contract and is easier to keep: any page that
// fails fails the call, and a payload that will not decompress fails it too
// rather than dropping a key. A key absent from the returned map holds no
// record, which is what makes the result a snapshot.
func (s *KubernetesStore) GetAll(ctx context.Context, keyPrefix string) (map[string]Record, error) {
	_, records, err := s.list(ctx, keyPrefix, true)
	if err != nil {
		return nil, err
	}
	return records, nil
}

// compressRecord and uncompressRecord are the stock kubernetes backend's
// compressState/uncompressState (internal/backend/remote-state/kubernetes/client.go),
// minus the base64 step: that backend reads a Secret through the dynamic
// client, where data values arrive as base64 strings, and this one uses the
// typed client, where corev1.Secret.Data is []byte and the API machinery does
// the base64 itself. The gzip half is the same, and the "encoding: gzip"
// annotation is spelled the same, so an operator who has read one object can
// read the other.
func compressRecord(data []byte) ([]byte, error) {
	b := new(bytes.Buffer)
	gz := gzip.NewWriter(b)
	if _, err := gz.Write(data); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func uncompressRecord(data []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the payload is not gzip: %w", err)
	}
	out, err := io.ReadAll(gz)
	if err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return out, nil
}
