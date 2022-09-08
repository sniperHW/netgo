package network

/*
 * 对Socket interface的异步封装
 * 提供异步recv和send处理
 */

type AsynSocket struct {
	socket Socket
}
