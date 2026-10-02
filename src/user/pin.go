package user

// workerPinPrefix keeps pin records apart from the user profiles, which are
// stored under the bare uid in the same database.
const workerPinPrefix = "worker-pin:"

// GetWorkerPin returns the execution node that holds a signed-in user's
// workspace, as recorded by a gateway.
func GetWorkerPin(uid string) (string, bool) {
	if uid == "" {
		return "", false
	}
	value, err := GetUserDBHandle().Fetch([]byte(workerPinPrefix + uid))
	if err != nil || len(value) == 0 {
		return "", false
	}
	return string(value), true
}

// SetWorkerPin records the execution node that holds a user's workspace.
func SetWorkerPin(uid, node string) error {
	db := GetUserDBHandle()
	if err := db.Store([]byte(workerPinPrefix+uid), []byte(node)); err != nil {
		return err
	}
	return db.Commit()
}
