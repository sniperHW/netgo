package poolPackBufferPolicy

type policy struct {
	buff *[]byte
}

func New() *policy {
	return &policy{}
}

func (d *policy) OnUpdate(buff []byte) {
	if nil == d.buff {
		panic("nil == d.buff")
	}
	d.buff = &buff
}

func (d *policy) OnSendOK(n int) {
	if nil == d.buff {
		panic("nil == d.buff")
	}
	put(*d.buff)
	d.buff = nil
}

func (d *policy) GetBuffer() []byte {
	if nil == d.buff {
		buff := get()
		d.buff = &buff
	}
	return *d.buff
}

func (d *policy) OnSenderExit() {
	if nil != d.buff {
		put(*d.buff)
		d.buff = nil
	}
}
