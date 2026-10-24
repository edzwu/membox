package syncx

// Transfer moves the blob identified by digest from src to dst. If dst
// already has it, Transfer is a no-op returning transferred=false — hash
// hits mean zero bytes cross. Verification happens on both ends (Get reads
// verify, Put writes verify), so a corrupt source can never evict good
// bytes on the destination.
func Transfer(src, dst BlobStore, digest string) (transferred bool, err error) {
	if dst.Has(digest) {
		return false, nil
	}
	r, err := src.Get(digest)
	if err != nil {
		return false, err
	}
	defer func() { _ = r.Close() }()
	if err := dst.Put(digest, r); err != nil {
		return false, err
	}
	return true, nil
}
