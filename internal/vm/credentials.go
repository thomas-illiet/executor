package vm

import (
	"bufio"
	"encoding/base64"
	"errors"
	"os"
	"strings"
)

type Credentials struct {
	UID    string
	APIKey string
}

// Load reads credentials from the environment or boot file.
func LoadCredentials(path string) (Credentials, error) {
	creds := Credentials{
		UID:    os.Getenv("EXECUTOR_ARTIFACTORY_UID"),
		APIKey: os.Getenv("EXECUTOR_ARTIFACTORY_API_KEY"),
	}
	if creds.UID != "" && creds.APIKey != "" {
		return creds, nil
	}

	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return creds, nil
		}
		return creds, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		parsed := ParseLine(scanner.Text())
		if parsed.APIKey != "" {
			if creds.APIKey == "" {
				creds.APIKey = parsed.APIKey
			}
			if creds.UID == "" {
				creds.UID = parsed.UID
			}
		}
	}
	return creds, scanner.Err()
}

// ParseLine extracts Artifactory credentials from one boot file line.
func ParseLine(line string) Credentials {
	if !strings.HasPrefix(line, "ARTIFACTORY_API_KEY") {
		return Credentials{}
	}
	parts := strings.Fields(line)
	if len(parts) < 3 {
		return Credentials{}
	}
	return Credentials{APIKey: parts[1], UID: parts[2]}
}

// RegistryAuth returns the base64 registry auth token.
func (c Credentials) RegistryAuth() string {
	if c.UID == "" || c.APIKey == "" {
		return ""
	}
	return base64.StdEncoding.EncodeToString([]byte(c.UID + ":" + c.APIKey))
}
