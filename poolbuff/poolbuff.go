package poolbuff

type poolbuff struct {
	buff *[]byte
}

func New() *poolbuff {
	return &poolbuff{}
}

func (d *poolbuff) OnUpdate(buff []byte) {
	if nil == d.buff {
		panic("nil == d.buff")
	}
	d.buff = &buff
}

func (d *poolbuff) ReleaseBuffer() {
	if nil == d.buff {
		panic("nil == d.buff")
	}
	put(*d.buff)
	d.buff = nil
}

func (d *poolbuff) GetBuffer() []byte {
	if nil == d.buff {
		buff := get()
		d.buff = &buff
	}
	return *d.buff
}

func (d *poolbuff) Clear() {
	if nil != d.buff {
		put(*d.buff)
		d.buff = nil
	}
}
