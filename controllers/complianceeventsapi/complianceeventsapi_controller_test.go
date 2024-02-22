package complianceeventsapi

import (
	"net/url"
	"os"
	"path"
	"strings"
	"testing"

	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
)

func TestParseDBSecret(t *testing.T) {
	t.Parallel()

	const caContent = "some-ca"

	tests := []struct {
		name     string
		secret   corev1.Secret
		expected string
	}{
		{
			name: "connectionURL-no-ssl",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"connectionURL": []byte("postgresql://grc:grc@localhost/db?sslmode=disable"),
				},
			},
			expected: "postgresql://grc:grc@localhost/db?sslmode=disable&connect_timeout=5",
		},
		{
			name: "connectionURL-newline",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"connectionURL": []byte("postgresql://grc:grc@localhost/db?sslmode=disable\n"),
				},
			},
			expected: "postgresql://grc:grc@localhost/db?sslmode=disable&connect_timeout=5",
		},
		{
			name: "connectionURL-ssl-ca",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"ca":            []byte(caContent),
					"connectionURL": []byte("postgresql://grc:grc@localhost/db?sslmode=verify-full"),
				},
			},
			expected: "postgresql://grc:grc@localhost/db?sslmode=verify-full&connect_timeout=5",
		},
		{
			name: "connectionURL-custom-connect_timeout",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"connectionURL": []byte("postgresql://grc:grc@localhost/db?connect_timeout=30"),
				},
			},
			expected: "postgresql://grc:grc@localhost/db?connect_timeout=30",
		},
		{
			name: "separate-with-defaults",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":     []byte("grc"),
					"password": []byte("grc"),
					"host":     []byte("localhost"),
					"dbname":   []byte("db"),
				},
			},
			expected: "postgresql://grc:grc@localhost:5432/db?sslmode=verify-full&connect_timeout=5",
		},
		{
			name: "separate-no-defaults",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":     []byte("grc"),
					"password": []byte("grc"),
					"host":     []byte("localhost"),
					"port":     []byte("1234"),
					"dbname":   []byte("db"),
					"sslmode":  []byte("disable"),
				},
			},
			expected: "postgresql://grc:grc@localhost:1234/db?sslmode=disable&connect_timeout=5",
		},
		{
			name: "separate-with-ca",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":     []byte("grc"),
					"password": []byte("grc"),
					"host":     []byte("localhost"),
					"dbname":   []byte("db"),
					"ca":       []byte(caContent),
				},
			},
			expected: "postgresql://grc:grc@localhost:5432/db?sslmode=verify-full&connect_timeout=5",
		},
	}

	for _, test := range tests {
		test := test

		t.Run(
			test.name,
			func(t *testing.T) {
				t.Parallel()

				g := NewWithT(t)

				tempDir, err := os.MkdirTemp("", "test-compliance-events-store")
				g.Expect(err).ToNot(HaveOccurred())

				defer os.RemoveAll(tempDir)

				connectionURL, err := ParseDBSecret(&test.secret, tempDir)
				g.Expect(err).ToNot(HaveOccurred())

				caPath := path.Join(tempDir, "db-ca.crt")

				if strings.Contains(connectionURL, "sslrootcert") {
					g.Expect(connectionURL).To(Equal(test.expected + "&sslrootcert=" + url.QueryEscape(caPath)))

					readCA, err := os.ReadFile(caPath)
					g.Expect(err).ToNot(HaveOccurred())
					g.Expect(string(readCA)).To(Equal(caContent))
				} else {
					g.Expect(connectionURL).To(Equal(test.expected))
					_, err := os.Stat(caPath)
					g.Expect(err).To(MatchError(os.ErrNotExist))
				}
			},
		)
	}
}

func TestParseDBSecretErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		secret         corev1.Secret
		expectedSubStr string
	}{
		{
			name: "missing-all",
			secret: corev1.Secret{
				Data: map[string][]byte{},
			},
			expectedSubStr: "no user value was provided",
		},
		{
			name: "missing-user",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"password": []byte("grc"),
					"host":     []byte("localhost"),
					"dbname":   []byte("db"),
				},
			},
			expectedSubStr: "no user value was provided",
		},
		{
			name: "missing-password",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":   []byte("grc"),
					"host":   []byte("localhost"),
					"dbname": []byte("db"),
				},
			},
			expectedSubStr: "no password value was provided",
		},
		{
			name: "missing-host",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":     []byte("grc"),
					"password": []byte("grc"),
					"dbname":   []byte("db"),
				},
			},
			expectedSubStr: "no host value was provided",
		},
		{
			name: "missing-host",
			secret: corev1.Secret{
				Data: map[string][]byte{
					"user":     []byte("grc"),
					"password": []byte("grc"),
					"host":     []byte("localhost"),
				},
			},
			expectedSubStr: "no dbname value was provided",
		},
	}

	for _, test := range tests {
		test := test

		t.Run(
			test.name,
			func(t *testing.T) {
				t.Parallel()

				g := NewWithT(t)
				_, err := ParseDBSecret(&test.secret, "")
				g.Expect(err).To(MatchError(ErrInvalidDBSecret))
				g.Expect(err.Error()).To(ContainSubstring(test.expectedSubStr))
			},
		)
	}
}
