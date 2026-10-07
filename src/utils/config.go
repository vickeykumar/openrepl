package utils

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Server-side settings. They are read from the environment first, which is
// what Docker (--env-file), systemd (EnvironmentFile) and a local
// `set -a; . ./.env; set +a` provide. The git-config style file
// (/opt/gotty/.gitconfig, ~/.gitconfig, /etc/.gitconfig) stays as a fallback,
// so servers that are configured with it keep working.
//
//	OPENREPL_ENV             dev or production (the default); see IsDev
//	OPENREPL_ADMIN_EMAILS    comma-separated admin accounts; file: user.email
//	OPENREPL_OPENAI_API_KEY  the OpenAI key as it is; file: user.OpenaiAPIKey, base64
//	OPENREPL_OPENROUTER_API_KEY  the OpenRouter key as it is (env only); without it the
//	                         OpenRouter models are not offered
//	OPENREPL_MONGODB_URI     optional; keeps the admin settings in MongoDB (Atlas, a
//	                         mongodb+srv:// URI) instead of settings.json; a secret
//	OPENREPL_MONGODB_DB      the database for it, default openrepl
//	OPENREPL_SECRET optional; encrypts the API keys an admin saves in the dashboard
//	OPENREPL_HOST            the origin the chat proxy accepts; file: user.host
//	OPENREPL_FIREBASE_CONFIG the Firebase web app config the page signs in
//	                         with, as JSON or base64 of JSON; see FirebaseConfigFromEnv
const (
	EnvMode          = "OPENREPL_ENV"
	EnvAdminEmails   = "OPENREPL_ADMIN_EMAILS"
	EnvOpenAIKey     = "OPENREPL_OPENAI_API_KEY"
	EnvOpenRouterKey = "OPENREPL_OPENROUTER_API_KEY"
	EnvHost          = "OPENREPL_HOST"
	EnvMongoURI      = "OPENREPL_MONGODB_URI"
	EnvMongoDB       = "OPENREPL_MONGODB_DB"
	EnvSecret        = "OPENREPL_SECRET"
	// a Google service account key (JSON, base64 of it, or a file path) that
	// lets the server use the Firebase project's Firestore as its database
	EnvFirestoreCredentials = "OPENREPL_FIRESTORE_CREDENTIALS"
	// the project, when it is not the one the key belongs to (the emulator)
	EnvFirestoreProject = "OPENREPL_FIRESTORE_PROJECT"
	EnvFirebaseConfig   = "OPENREPL_FIREBASE_CONFIG"
)

// IsDev reports whether the server runs in development mode, that is
// OPENREPL_ENV is dev, development or local. Anything else, including nothing
// at all, is production. The only difference is how much the log says about
// the settings: production never prints their values.
func IsDev() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(EnvMode))) {
	case "dev", "development", "local":
		return true
	}
	return false
}

// gitConfigFiles lists where the file fallback is looked for, in order of
// priority. home is the user's home directory, "" when it is not known.
func gitConfigFiles(home string) []string {
	files := []string{filepath.Join(GOTTY_PATH, GitConfigFile)}
	if home != "" {
		files = append(files, filepath.Join(home, GitConfigFile))
	}
	return append(files, filepath.Join(SYSTEM_CONFIG_PATH, GitConfigFile))
}

func homeDir() string {
	if h, err := os.UserHomeDir(); err == nil {
		return h
	}
	return ""
}

// GitConfigPath is the file GitConfig was read from, "" if there was none.
var GitConfigPath string

// GetGitConfig reads the file fallback: the first of the files above that can
// be read, flattened into "section.key" pairs.
func GetGitConfig() map[string]string {
	config, path := readGitConfig(gitConfigFiles(homeDir()))
	GitConfigPath = path
	return config
}

// readGitConfig returns the pairs of the first file that can be read, and
// which file it was. A file that does not exist is skipped without a word;
// one that exists but cannot be read is reported.
func readGitConfig(files []string) (map[string]string, string) {
	for _, file := range files {
		data, err := ioutil.ReadFile(file)
		if err != nil {
			if !os.IsNotExist(err) {
				log.Printf("Error: cannot read config file %s: %s", file, err.Error())
			}
			continue
		}
		return parseGitConfig(data), file
	}
	return map[string]string{}, ""
}

// parseGitConfig flattens git-config text into "section.key" pairs. A
// subsection joins with a dot: [user "x"] with a key k gives user.x.k.
func parseGitConfig(data []byte) map[string]string {
	config := make(map[string]string)
	scanner := bufio.NewScanner(bytes.NewReader(data))
	section := ""
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			parts := strings.Split(strings.TrimSpace(line[1:len(line)-1]), " ")
			for i, part := range parts {
				parts[i] = strings.Trim(part, "\"")
			}
			section = strings.Join(parts, ".")
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key := strings.TrimSpace(kv[0])
		if section != "" {
			key = section + "." + key
		}
		config[key] = strings.TrimSpace(kv[1])
	}
	return config
}

// splitList splits a comma-separated list, trimming spaces and dropping
// empty items.
func splitList(s string) []string {
	var out []string
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// AdminEmails returns the accounts that are admins: OPENREPL_ADMIN_EMAILS if
// it is set, otherwise the file's user.email. An empty result means that
// nobody is an admin.
func AdminEmails() []string {
	if v := strings.TrimSpace(os.Getenv(EnvAdminEmails)); v != "" {
		return splitList(v)
	}
	if e := strings.TrimSpace(GitConfig["user.email"]); e != "" {
		return []string{e}
	}
	return nil
}

// IsOwnerEmail reports whether email is one of the accounts configured in the
// environment (AdminEmails). Only owners can add or remove other admins in the
// dashboard. The comparison ignores case, and an empty email is never one.
func IsOwnerEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	for _, admin := range AdminEmails() {
		if strings.EqualFold(admin, email) {
			return true
		}
	}
	return false
}

var (
	adminMu     sync.RWMutex
	extraAdmins []string
)

// SetExtraAdmins sets the admins added in the dashboard (settings.go). Owners
// are not among them: those come from the environment.
func SetExtraAdmins(emails []string) {
	adminMu.Lock()
	extraAdmins = append([]string(nil), emails...)
	adminMu.Unlock()
}

// ExtraAdmins returns the admins added in the dashboard.
func ExtraAdmins() []string {
	adminMu.RLock()
	defer adminMu.RUnlock()
	return append([]string(nil), extraAdmins...)
}

// IsAdminEmail reports whether email belongs to an admin: an owner, or an admin
// added in the dashboard. The comparison ignores case. No admin at all means
// no admin, and an empty email is never one.
func IsAdminEmail(email string) bool {
	email = strings.TrimSpace(email)
	if email == "" {
		return false
	}
	if IsOwnerEmail(email) {
		return true
	}
	for _, admin := range ExtraAdmins() {
		if strings.EqualFold(admin, email) {
			return true
		}
	}
	return false
}

// Keys an admin saved in the dashboard. They come from the settings store
// (server/settings_keys.go), decrypted in memory, and win over the
// environment's.
var (
	keyMu             sync.RWMutex
	openAIOverride    string
	openRouterOverr   string
	secretFromGateway string
	generatedSecret   string
)

// SetKeyOverrides sets the keys saved in the dashboard; "" means none.
func SetKeyOverrides(openai, openrouter string) {
	keyMu.Lock()
	openAIOverride, openRouterOverr = strings.TrimSpace(openai), strings.TrimSpace(openrouter)
	keyMu.Unlock()
}

// KeySource says where the key in use comes from: "dashboard", "env", "file",
// or "" when there is none. provider is "openai" or "openrouter".
func KeySource(provider string) string {
	keyMu.RLock()
	over := map[string]string{"openai": openAIOverride, "openrouter": openRouterOverr}[provider]
	keyMu.RUnlock()
	switch {
	case over != "":
		return "dashboard"
	case provider == "openai":
		return source(EnvOpenAIKey, "user.OpenaiAPIKey")
	case provider == "openrouter":
		return source(EnvOpenRouterKey, "")
	}
	return ""
}

// OpenAIKey returns the OpenAI API key: the one saved in the dashboard, else
// OPENREPL_OPENAI_API_KEY as it is, or else the file's user.OpenaiAPIKey, which
// is base64-encoded. It is empty when there is none, or the file's is not valid
// base64.
func OpenAIKey() string {
	keyMu.RLock()
	over := openAIOverride
	keyMu.RUnlock()
	if over != "" {
		return over
	}
	if v := strings.TrimSpace(os.Getenv(EnvOpenAIKey)); v != "" {
		return v
	}
	decoded, err := base64.StdEncoding.DecodeString(GitConfig["user.OpenaiAPIKey"])
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(decoded))
}

// OpenRouterKey returns the OpenRouter API key: the one saved in the dashboard,
// else OPENREPL_OPENROUTER_API_KEY as it is, or "" when neither is set. There
// is no file fallback: the setting is newer than the git-config style file.
// Without it the OpenRouter models (Gemma 4 31B) are not offered.
func OpenRouterKey() string {
	keyMu.RLock()
	over := openRouterOverr
	keyMu.RUnlock()
	if over != "" {
		return over
	}
	return strings.TrimSpace(os.Getenv(EnvOpenRouterKey))
}

// FirestoreConfigured reports whether Firestore was asked for: a service
// account key, or the emulator, is set.
func FirestoreConfigured() bool {
	return strings.TrimSpace(os.Getenv(EnvFirestoreCredentials)) != "" || strings.TrimSpace(os.Getenv("FIRESTORE_EMULATOR_HOST")) != ""
}

// MongoURI is OPENREPL_MONGODB_URI, as it is. When set, the gateway or the
// standalone server keeps the admin settings in that database and not in
// settings.json. It holds a password, so it is only ever reported as set or
// not set.
func MongoURI() string {
	return strings.TrimSpace(os.Getenv(EnvMongoURI))
}

// MongoDBName is the database the settings live in: OPENREPL_MONGODB_DB or
// "openrepl".
func MongoDBName() string {
	if v := strings.TrimSpace(os.Getenv(EnvMongoDB)); v != "" {
		return v
	}
	return "openrepl"
}

// Secret is the server's secret, for its own use (long and random, never shown
// anywhere). Today it encrypts the API keys an admin saves in the dashboard.
// In order: the gateway's, on a worker that has connected (SetSecretFromGateway);
// OPENREPL_SECRET from the environment; the one the server made and saved in
// its database when the environment has none (SetGeneratedSecret).
func Secret() string {
	keyMu.RLock()
	over, gen := secretFromGateway, generatedSecret
	keyMu.RUnlock()
	if over != "" {
		return over
	}
	if v := EnvSecretValue(); v != "" {
		return v
	}
	return gen
}

// EnvSecretValue is OPENREPL_SECRET as the environment has it, "" when it is
// not set.
func EnvSecretValue() string {
	return os.Getenv(EnvSecret)
}

// SetGeneratedSecret records the secret the server made and saved in its
// database because OPENREPL_SECRET is not set.
func SetGeneratedSecret(secret string) {
	keyMu.Lock()
	generatedSecret = secret
	keyMu.Unlock()
}

// SetSecretFromGateway sets the secret a worker received from its gateway;
// "" (the gateway has none) leaves the worker's own in use.
func SetSecretFromGateway(secret string) {
	keyMu.Lock()
	secretFromGateway = secret
	keyMu.Unlock()
}

// Host returns the origin the chat proxy accepts: OPENREPL_HOST, or else the
// file's user.host. It is empty when neither is set.
func Host() string {
	if v := strings.TrimSpace(os.Getenv(EnvHost)); v != "" {
		return v
	}
	return strings.TrimSpace(GitConfig["user.host"])
}

// The fields of a Firebase web app config (Project settings, Your apps, the
// config object) that the page accepts. The first three are required.
var firebaseKeys = []string{"apiKey", "authDomain", "projectId", "databaseURL", "storageBucket", "messagingSenderId", "appId", "measurementId"}

var (
	firebaseRequired = []string{"apiKey", "authDomain", "projectId"}
	apiKeyPattern    = regexp.MustCompile(`^[A-Za-z0-9_-]{8,200}$`)
	hostPattern      = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[0-9]{1,5})?$`)
	projectPattern   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{3,60}$`)
	dbURLPattern     = regexp.MustCompile(`^(https://[A-Za-z0-9.-]+|http://(localhost|127\.0\.0\.1)(:[0-9]{1,5})?)(/?\?ns=[A-Za-z0-9-]+)?/?$`)
	plainPattern     = regexp.MustCompile(`^[A-Za-z0-9:_.-]{1,200}$`)
)

// FirebaseConfigFromEnv returns the Firebase web app config from
// OPENREPL_FIREBASE_CONFIG, so that a development project can be used instead
// of the production one the server has built in. set is false when the
// variable is empty. The value can be any of
//
//   - the config object as JSON:
//     {"apiKey":"...","authDomain":"x.firebaseapp.com","projectId":"x"}
//   - the snippet the Firebase console shows, a JavaScript object with bare
//     keys, with or without the `const firebaseConfig =` in front of it
//   - the base64 of either, which survives places that do not understand
//     quotes, such as a Docker --env-file. The server's own built-in config is
//     kept the same way: base64 of that snippet.
//
// apiKey, authDomain and projectId are required; fields that are not part of
// a web app config are ignored. The snippet is read for its key: "value" pairs
// only. Nothing in it is run.
//
// Every value is checked against the shape it must have, because it ends up in
// a script that every visitor runs. A wrong value is an error, never a quiet
// fall back to the built-in project; the error does not repeat the value.
func FirebaseConfigFromEnv() (cfg map[string]string, set bool, err error) {
	raw := strings.TrimSpace(os.Getenv(EnvFirebaseConfig))
	if raw == "" {
		return nil, false, nil
	}
	text := raw
	if !strings.HasPrefix(raw, "{") && !strings.Contains(raw, "apiKey") {
		decoded, derr := base64.StdEncoding.DecodeString(raw)
		if derr != nil {
			return nil, true, fmt.Errorf("%s must be the config as JSON or as the snippet the Firebase console shows, or base64 of either (if the value is wrapped in quotes, remove them: Docker --env-file keeps quotes as part of the value)", EnvFirebaseConfig)
		}
		text = string(decoded)
	}
	fields, ferr := firebaseFields(text)
	if ferr != nil {
		return nil, true, fmt.Errorf("%s is not valid JSON and not a Firebase config snippet (check the quotes)", EnvFirebaseConfig)
	}
	cfg = make(map[string]string)
	for _, key := range firebaseKeys {
		v, present := fields[key]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			return nil, true, fmt.Errorf("%s: %q must be a string", EnvFirebaseConfig, key)
		}
		if s = strings.TrimSpace(s); s != "" {
			cfg[key] = s
		}
	}
	var missing []string
	for _, key := range firebaseRequired {
		if cfg[key] == "" {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return nil, true, fmt.Errorf("%s lacks %s", EnvFirebaseConfig, strings.Join(missing, ", "))
	}
	checks := map[string]*regexp.Regexp{
		"apiKey": apiKeyPattern, "authDomain": hostPattern, "projectId": projectPattern, "databaseURL": dbURLPattern,
		"storageBucket": hostPattern, "messagingSenderId": plainPattern, "appId": plainPattern, "measurementId": plainPattern,
	}
	var keys []string
	for key := range cfg {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !checks[key].MatchString(cfg[key]) {
			return nil, true, fmt.Errorf("%s: %q does not look like a Firebase %s", EnvFirebaseConfig, key, key)
		}
	}
	return cfg, true, nil
}

// snippetPair finds key: "value" and key: 'value' in the object the Firebase
// console shows. A value with a backslash in it does not match, so a value
// cannot carry an escape into the page.
var snippetPair = regexp.MustCompile(`(?:^|[\s{,])["']?([A-Za-z][A-Za-z0-9]*)["']?\s*:\s*(?:"([^"\\\r\n]*)"|'([^'\\\r\n]*)')`)

// firebaseFields reads the fields of a config given as JSON or as the
// snippet from the console.
func firebaseFields(text string) (map[string]interface{}, error) {
	if trimmed := strings.TrimSpace(text); strings.HasPrefix(trimmed, "{") {
		var fields map[string]interface{}
		if json.Unmarshal([]byte(trimmed), &fields) == nil {
			return fields, nil
		}
	}
	fields := make(map[string]interface{})
	for _, m := range snippetPair.FindAllStringSubmatch(text, -1) {
		value := m[2]
		if value == "" {
			value = m[3]
		}
		fields[m[1]] = value
	}
	if len(fields) == 0 {
		return nil, fmt.Errorf("no fields")
	}
	return fields, nil
}

// FirebaseConfigJS returns the script that gives the page its Firebase config
// when OPENREPL_FIREBASE_CONFIG is set and valid, and false otherwise. The
// object is written with encoding/json, so a value cannot break out of it.
func FirebaseConfigJS() ([]byte, bool) {
	cfg, set, err := FirebaseConfigFromEnv()
	if err != nil || !set {
		return nil, false
	}
	obj, merr := json.MarshalIndent(cfg, "  ", "  ")
	if merr != nil {
		return nil, false
	}
	return []byte("\nconst firebaseconfig = " + string(obj) + ";\n\n"), true
}

// source says where a setting comes from: "env", "file" or "" when unset.
func source(envName, fileKey string) string {
	if strings.TrimSpace(os.Getenv(envName)) != "" {
		return "env"
	}
	if strings.TrimSpace(GitConfig[fileKey]) != "" {
		return "file"
	}
	return ""
}

// configSummary says which settings are set and where from. In production it
// never shows a value, not even an email address. In development it shows
// them, which is what makes a wrong setting easy to find.
func configSummary(dev bool) string {
	var b strings.Builder
	if dev {
		b.WriteString("config: mode=dev (values are shown)")
	} else {
		b.WriteString("config: mode=production (values are not shown)")
	}
	if GitConfigPath != "" {
		fmt.Fprintf(&b, "; file=%s", GitConfigPath)
	} else {
		b.WriteString("; file=none")
	}
	if r := LastEnvFile; r.Path != "" {
		fmt.Fprintf(&b, "; env file=%s (%d loaded", r.Path, len(r.Loaded))
		if len(r.Kept) > 0 {
			fmt.Fprintf(&b, ", %d kept because the environment already had them", len(r.Kept))
		}
		if len(r.Ignored) > 0 {
			fmt.Fprintf(&b, ", %d ignored, not OPENREPL_ or GOTTY_: %s", len(r.Ignored), strings.Join(r.Ignored, ","))
		}
		b.WriteString(")")
	} else {
		b.WriteString("; env file=none")
	}
	item := func(name, envName, fileKey string, value string, count int) {
		src := source(envName, fileKey)
		if src == "" {
			fmt.Fprintf(&b, "; %s: not set", name)
			return
		}
		switch {
		case dev:
			fmt.Fprintf(&b, "; %s: %s (from %s)", name, value, src)
		case count > 0:
			fmt.Fprintf(&b, "; %s: set, %d (from %s)", name, count, src)
		default:
			fmt.Fprintf(&b, "; %s: set (from %s)", name, src)
		}
	}
	admins := AdminEmails()
	item("admin emails", EnvAdminEmails, "user.email", strings.Join(admins, ","), len(admins))
	item("openai key", EnvOpenAIKey, "user.OpenaiAPIKey", OpenAIKey(), 0)
	item("openrouter key", EnvOpenRouterKey, "", OpenRouterKey(), 0)
	item("host", EnvHost, "user.host", Host(), 0)
	// the URI holds a password: never its value, not even in dev
	switch {
	case MongoURI() != "":
		fmt.Fprintf(&b, "; data store: mongodb, database %s (from env), else firestore if configured, else file", MongoDBName())
	case FirestoreConfigured():
		b.WriteString("; data store: firestore if its project answers, else file")
	default:
		b.WriteString("; data store: file")
	}
	if Secret() == "" {
		b.WriteString("; secret: not set in the environment (one is generated and saved in the database)")
	} else {
		b.WriteString("; secret: set")
	}
	switch fb, set, err := FirebaseConfigFromEnv(); {
	case err != nil:
		fmt.Fprintf(&b, "; firebase: INVALID (%s)", err.Error())
	case !set && dev:
		b.WriteString("; firebase: the built-in project, which is the production one (set " + EnvFirebaseConfig + " to use a development project)")
	case !set:
		b.WriteString("; firebase: the built-in project")
	case dev:
		obj, _ := json.Marshal(fb)
		fmt.Fprintf(&b, "; firebase: %s (from env)", obj)
	default:
		b.WriteString("; firebase: custom project (from env)")
	}
	if len(admins) == 0 {
		b.WriteString("; nobody is an admin")
	}
	return b.String()
}

// LogConfig writes configSummary to the log.
func LogConfig() { log.Println(configSummary(IsDev())) }
