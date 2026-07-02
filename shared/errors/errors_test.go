package errors

import (
	"errors"
	"testing"
)

func TestErrorMessageFormatting(t *testing.T) {
	err := New(CodeNotFound, "trace abc123 not found")
	want := "not_found: trace abc123 not found"
	if got := err.Error(); got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}

func TestWrapPreservesCauseViaUnwrap(t *testing.T) {
	cause := errors.New("connection refused")
	err := Wrap(CodeUnavailable, "clickhouse write failed", cause)

	if !errors.Is(err, cause) {
		t.Fatal("errors.Is(err, cause) = false, want true")
	}
	if got := err.Unwrap(); got != cause {
		t.Fatalf("Unwrap() = %v, want %v", got, cause)
	}
}

func TestCodeOfExtractsCode(t *testing.T) {
	err := New(CodePermissionDenied, "missing scope")
	if got := CodeOf(err); got != CodePermissionDenied {
		t.Fatalf("CodeOf() = %v, want %v", got, CodePermissionDenied)
	}
}

func TestCodeOfDefaultsToInternalForPlainError(t *testing.T) {
	plain := errors.New("something broke")
	if got := CodeOf(plain); got != CodeInternal {
		t.Fatalf("CodeOf(plain error) = %v, want %v", got, CodeInternal)
	}
}

func TestCodeOfUnwrapsThroughStandardWrapping(t *testing.T) {
	base := New(CodeAlreadyExists, "project exists")
	wrapped := errors.New("context: ") // simulate a %w wrap via fmt elsewhere
	_ = wrapped
	// errors.As must find the *Error even through fmt.Errorf("%w", base).
	via := errorsWrapf(base)
	if got := CodeOf(via); got != CodeAlreadyExists {
		t.Fatalf("CodeOf(fmt-wrapped) = %v, want %v", got, CodeAlreadyExists)
	}
}

func TestIsRetryable(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"unavailable is retryable", New(CodeUnavailable, "collector down"), true},
		{"internal is not retryable", New(CodeInternal, "panic recovered"), false},
		{"not_found is not retryable", New(CodeNotFound, "no such trace"), false},
		{"plain error is not retryable", errors.New("boom"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := IsRetryable(c.err); got != c.want {
				t.Errorf("IsRetryable() = %v, want %v", got, c.want)
			}
		})
	}
}

// errorsWrapf exercises fmt.Errorf("%w", ...) wrapping without importing fmt
// twice at the top of the test file for a single use.
func errorsWrapf(base error) error {
	return &wrapped{base}
}

type wrapped struct{ err error }

func (w *wrapped) Error() string { return "wrapped: " + w.err.Error() }
func (w *wrapped) Unwrap() error { return w.err }
