package poolbuff

//go test -race -covermode=atomic -v -coverprofile=coverage.out -run=.
//go tool cover -html=coverage.out
import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPolycy(t *testing.T) {
	poolbuff := New()
	buff := poolbuff.GetBuffer()
	assert.Equal(t, 0, len(buff))

	buff = append(buff, []byte("hello")...)

	poolbuff.OnUpdate(buff)

	buff = poolbuff.GetBuffer()
	assert.Equal(t, 5, len(buff))

	poolbuff.ReleaseBuffer()

	buff = poolbuff.GetBuffer()
	assert.Equal(t, 0, len(buff))

	poolbuff.ReleaseBuffer()

}
