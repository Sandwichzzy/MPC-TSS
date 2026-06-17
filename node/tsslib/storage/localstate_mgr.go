package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Sandwichzzy/tss/node/tsslib/conversion"
	"github.com/Sandwichzzy/tss/node/tsslib/p2p"
	"github.com/bnb-chain/tss-lib/ecdsa/keygen"
	"github.com/libp2p/go-libp2p/core/peer"
	ma "github.com/multiformats/go-multiaddr"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// localstate_mgr.go 是默认的本地文件后端。
// 不启用云端托管时，节点的 keygen 结果、预计算参数和已知 peer 地址都落在这里。
const PreParams = "pre_params"

type KeygenLocalState struct {
	PubKey          string                    `json:"pub_key"`          // 聚合压缩公钥，后续 keysign 会用它作为检索键找到本地份额状态。
	LocalData       keygen.LocalPartySaveData `json:"local_data"`       // tss-lib 生成的本地私钥份额状态，真正的本地秘密材料都封装在这里。
	ParticipantKeys []string                  `json:"participant_keys"` // 这把聚合公钥最初由哪些 participant 共同生成，后续签名时会校验 signer 集合是否属于这里。
	LocalPartyKey   string                    `json:"local_party_key"`  // 当前节点自己的公钥，用来区分本地这一份是 participant 集合里的哪一个节点。
	Threshold       int                       `json:"threshold"`        // 本次 keygen 使用的门限参数。
}

// ILocalStateManager 抽象“keygen 本地状态存到哪里”。 当前仓库有本地文件、SecretsManager 和 Shamir 三套实现。
type ILocalStateManager interface {
	SaveLocalState(state KeygenLocalState) error
	GetLocalState(pubKey string) (KeygenLocalState, error)
	SaveAddressBook(addressBook map[peer.ID]p2p.AddrList) error
	RetrieveP2PAddresses() (p2p.AddrList, error)
	SavePreParams(preParams *keygen.LocalPreParams) error
	GetLocalPreParams(string) (*keygen.LocalPreParams, error)
}

type FileStateMgr struct {
	folder    string
	logger    zerolog.Logger
	writeLock *sync.RWMutex
}

// NewFileStateMgr 会准备本地目录，后续三个文件族都会写到这个目录里：
// 1. localstate-<pubkey>.json
// 2. pre_params.json
// 3. address_book.seed
func NewFileStateMgr(folder string) (*FileStateMgr, error) {
	if len(folder) > 0 {
		_, err := os.Stat(folder)
		if err != nil && os.IsNotExist(err) {
			if err := os.MkdirAll(folder, os.ModePerm); err != nil {
				return nil, err
			}
		}
	}
	return &FileStateMgr{
		folder:    folder,
		logger:    log.With().Str("module", "storage").Logger(),
		writeLock: &sync.RWMutex{},
	}, nil
}

func (fsm *FileStateMgr) getFilePathName(pubKey string) (string, error) {
	// 以聚合公钥派生文件名之前，先校验它确实是曲线上的合法公钥，避免脏数据落盘。
	ret, err := conversion.CheckKeyOnCurve(pubKey)
	if err != nil {
		return "", err
	}
	if !ret {
		return "", errors.New("invalid pubkey for file name")
	}

	localFileName := fmt.Sprintf("localstate-%s.json", pubKey)
	if len(fsm.folder) > 0 {
		return filepath.Join(fsm.folder, localFileName), nil
	}
	return localFileName, nil
}

// 这里不是按特定 pubkey 查文件，而是随便拿到一份历史 localstate。
// getOneFilePathName 主要用途是从旧状态里提取 LocalPreParams，避免节点启动时重新生成 preparams。
func (fsm *FileStateMgr) getOneFilePathName() (string, error) {
	var pattern = "localstate*.json"
	if len(fsm.folder) > 0 {
		pattern = filepath.Join(fsm.folder, pattern)
	}
	files, err := filepath.Glob(pattern)
	if err != nil {
		return "", err
	}
	if len(files) > 0 {
		return files[0], nil
	}
	return "", nil
}

// SaveLocalState 写入完整的 keygen 本地状态。keysign 阶段会直接读取这份 JSON 来恢复本地份额。
func (fsm *FileStateMgr) SaveLocalState(state KeygenLocalState) error {
	buf, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("fail to marshal KeygenLocalState to json: %w", err)
	}
	filePathName, err := fsm.getFilePathName(state.PubKey)
	if err != nil {
		return err
	}
	return os.WriteFile(filePathName, buf, 0o655)
}

// GetLocalState 按聚合公钥读回完整的 keygen 本地状态。
func (fsm *FileStateMgr) GetLocalState(pubKey string) (KeygenLocalState, error) {
	if len(pubKey) == 0 {
		return KeygenLocalState{}, errors.New("pub key is empty")
	}
	filePathName, err := fsm.getFilePathName(pubKey)
	if err != nil {
		return KeygenLocalState{}, err
	}
	if _, err := os.Stat(filePathName); os.IsNotExist(err) {
		return KeygenLocalState{}, err
	}

	buf, err := os.ReadFile(filePathName)
	if err != nil {
		return KeygenLocalState{}, fmt.Errorf("file to read from file(%s): %w", filePathName, err)
	}
	var localState KeygenLocalState

	if err := json.Unmarshal(buf, &localState); err != nil {
		return KeygenLocalState{}, fmt.Errorf("fail to unmarshal KeygenLocalState: %w", err)
	}
	return localState, nil
}

// GetOneLocalPreParams 从任意一份已有 localstate 中抽取 preparams。
// 这是启动阶段“有旧状态就重用预计算”的关键入口之一。
func (fsm *FileStateMgr) GetOneLocalPreParams() (*keygen.LocalPreParams, error) {
	filePathName, err := fsm.getOneFilePathName()
	if err != nil {
		return nil, err
	}
	if len(filePathName) == 0 {
		return nil, os.ErrNotExist
	}
	if _, err := os.Stat(filePathName); os.IsNotExist(err) {
		return nil, err
	}
	buf, err := os.ReadFile(filePathName)
	if err != nil {
		return nil, fmt.Errorf("file to read from file(%s): %w", filePathName, err)
	}
	var localState KeygenLocalState
	if err := json.Unmarshal(buf, &localState); nil != err {
		return nil, fmt.Errorf("fail to unmarshal KeygenLocalState: %w", err)
	}
	return &localState.LocalData.LocalPreParams, nil
}

// SaveAddressBook 保存已知 peer 地址簿，供下次启动时补充 bootstrap peers。
func (fsm *FileStateMgr) SaveAddressBook(address map[peer.ID]p2p.AddrList) error {
	if len(fsm.folder) < 1 {
		return errors.New("base file path is invalid")
	}
	filePathName := filepath.Join(fsm.folder, "address_book.seed")
	var buf bytes.Buffer
	for peer, addrs := range address {
		for _, addr := range addrs {
			// we do not save the loopback addr
			if strings.Contains(addr.String(), "127.0.0.1") {
				continue
			}
			record := addr.String() + "/p2p/" + peer.String() + "\n"
			_, err := buf.WriteString(record)
			if err != nil {
				return errors.New("fail to write the record to buffer")
			}
		}
	}
	fsm.writeLock.Lock()
	defer fsm.writeLock.Unlock()
	return os.WriteFile(filePathName, buf.Bytes(), 0o655)
}

// RetrieveP2PAddresses 读取上次运行保存的地址簿，NewTss(...) 会把这些地址和配置文件中的 bootstrap peers 合并使用。
func (fsm *FileStateMgr) RetrieveP2PAddresses() (p2p.AddrList, error) {
	if len(fsm.folder) < 1 {
		return nil, errors.New("base file path is invalid")
	}
	filePathName := filepath.Join(fsm.folder, "address_book.seed")
	_, err := os.Stat(filePathName)
	if err != nil {
		return nil, err
	}
	fsm.writeLock.RLock()
	input, err := os.ReadFile(filePathName)
	if err != nil {
		fsm.writeLock.RUnlock()
		return nil, err
	}
	fsm.writeLock.RUnlock()
	data := strings.Split(string(input), "\n")
	var peerAddresses []ma.Multiaddr
	for _, el := range data {
		// we skip the empty entry
		if len(el) == 0 {
			continue
		}
		addr, err := ma.NewMultiaddr(el)
		if err != nil {
			return nil, fmt.Errorf("invalid address in address book %w", err)
		}
		peerAddresses = append(peerAddresses, addr)
	}
	return peerAddresses, nil
}

// SavePreParams 单独保存 preparams 文件。这条路径更多服务于显式预生成文件的场景。
func (fsm *FileStateMgr) SavePreParams(preParams *keygen.LocalPreParams) error {
	buf, err := json.Marshal(preParams)
	if err != nil {
		return fmt.Errorf("fail to marshal keygen local preparams to json: %w", err)
	}
	localFileName := fmt.Sprintf("%s.json", PreParams)
	var filePathName = localFileName
	if len(fsm.folder) > 0 {
		filePathName = filepath.Join(fsm.folder, localFileName)
	}
	return os.WriteFile(filePathName, buf, 0o655)
}

// GetLocalPreParams 显式加载 pre_params.json 或调用方指定的 preparams 文件。
func (fsm *FileStateMgr) GetLocalPreParams(filePath string) (*keygen.LocalPreParams, error) {
	if len(filePath) == 0 {
		filePath = filepath.Join(fsm.folder, fmt.Sprintf("%s.json", PreParams))
	}
	buf, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}
	buf = bytes.Trim(buf, "\n")
	var preParam keygen.LocalPreParams
	if err = json.Unmarshal(buf, &preParam); err != nil {
		log.Error().Err(err).Msg("fail to unmarshal file content to LocalPreParams")
		return nil, err
	}
	return &preParam, nil
}
