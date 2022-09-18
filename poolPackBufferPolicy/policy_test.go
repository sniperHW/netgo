package poolPackBufferPolicy

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolycy(t *testing.T) {
	policy := New()
	buff := policy.GetBuffer()
	assert.Equal(t, 0, len(buff))

	buff = append(buff, []byte("hello")...)

	policy.OnUpdate(buff)

	buff = policy.GetBuffer()
	assert.Equal(t, 5, len(buff))

	policy.OnSendOK(len(buff))

	buff = policy.GetBuffer()
	assert.Equal(t, 0, len(buff))

	policy.OnSenderExit()

}
