package abnormal

import (
	"bytes"
	"fmt"
	"sync"
)

const (
	maxAbnormalNodeLength = 100
)

// NewNode 构造一条异常节点记录，包含节点公钥、相关数据和签名。
func NewNode(pk string, data, sig []byte) *Node {
	return &Node{
		Pubkey:    pk,
		Data:      data,
		Signature: sig,
	}
}

// Equal 判断两条异常节点记录是否指向同一个节点及其同一份签名数据。
func (n *Node) Equal(node *Node) bool {
	if node == nil {
		return false
	}
	if n.Pubkey == node.Pubkey && bytes.Equal(n.Signature, node.Signature) {
		return true
	}
	return false
}

// NewAbnormal 创建一个新的异常状态对象，并预先写入首批异常节点。
func NewAbnormal(reason string, nodes []*Node) *Abnormal {
	abnormal := &Abnormal{
		FailReason:   reason,
		Nodes:        make([]*Node, 0, maxAbnormalNodeLength),
		AbnormalLock: sync.RWMutex{},
	}
	abnormal.appendNewNodes(nodes)
	return abnormal
}

func (a *Abnormal) String() string {
	//sb := strings.Builder{}
	//sb.WriteString("reason:")
	//sb.WriteString(a.FailReason)
	//
	//sb.WriteString(" is_unicast:")
	//sb.WriteString(strconv.FormatBool(a.IsUnicast))
	//sb.WriteByte('\n')
	//
	//sb.WriteString("nodes:")
	//sb.WriteString(fmt.Sprintf("%+v", a.Nodes))
	//sb.WriteByte('\n')
	//return sb.String()
	return fmt.Sprintf(
		"reason:%s is_unicast:%v\nnodes:%+v\n",
		a.FailReason,
		a.IsUnicast,
		a.Nodes,
	)
}

// appendNewNodes 以固定上限追加异常节点，超过限制时只保留最新的数据。
func (a *Abnormal) appendNewNodes(newNodes []*Node) {
	if len(newNodes) > maxAbnormalNodeLength {
		//丢掉旧的 a.Nodes,只保留 newNodes 的最后 100 条
		a.Nodes = newNodes[len(newNodes)-maxAbnormalNodeLength:]
	} else if len(newNodes)+len(a.Nodes) > maxAbnormalNodeLength {
		exceedAmount := len(newNodes) + len(a.Nodes) - maxAbnormalNodeLength
		//丢掉最老的 exceedAmount 条
		a.Nodes = a.Nodes[exceedAmount:]
		a.Nodes = append(a.Nodes, newNodes...)
	} else {
		a.Nodes = append(a.Nodes, newNodes...)
	}
}

// SetAbnormal 在加锁状态下更新异常原因、消息类型及异常节点列表。
func (a *Abnormal) SetAbnormal(reason string, nodes []*Node, isUnicast bool) {
	a.AbnormalLock.Lock()
	defer a.AbnormalLock.Unlock()
	a.FailReason = reason
	a.IsUnicast = isUnicast
	a.appendNewNodes(nodes)
}
