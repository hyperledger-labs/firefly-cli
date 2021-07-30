package geth

import (
	"fmt"
	"strings"
)

type Genesis struct {
	Config     *GenesisConfig    `json:"config"`
	Nonce      string            `json:"nonce"`
	Timestamp  string            `json:"timestamp"`
	ExtraData  string            `json:"extraData"`
	GasLimit   string            `json:"gasLimit"`
	Difficulty string            `json:"difficulty"`
	MixHash    string            `json:"mixHash"`
	Coinbase   string            `json:"coinbase"`
	Alloc      map[string]*Alloc `json:"alloc"`
	Number     string            `json:"number"`
	GasUsed    string            `json:"gasUsed"`
	ParentHash string            `json:"parentHash"`
}

type GenesisConfig struct {
	ChainId             int           `json:"chainId"`
	HomesteadBlock      int           `json:"homesteadBlock"`
	Eip150Block         int           `json:"eip150Block"`
	Eip150Hash          string        `json:"eip150Hash"`
	Eip155Block         int           `json:"eip155Block"`
	Eip158Block         int           `json:"eip158Block"`
	ByzantiumBlock      int           `json:"byzantiumBlock"`
	ConstantinopleBlock int           `json:"constantinopleBlock"`
	PetersburgBlock     int           `json:"petersburgBlock"`
	IstanbulBlock       int           `json:"istanbulBlock"`
	Clique              *CliqueConfig `json:"clique"`
}

type CliqueConfig struct {
	Period int `json:"period"`
	Epoch  int `json:"epoch"`
}

type Alloc struct {
	Balance string `json:"balance"`
}

func CreateGenesisJson(addresses []string) *Genesis {

	extraData := "0x0000000000000000000000000000000000000000000000000000000000000000"
	alloc := make(map[string]*Alloc)

	for _, address := range addresses {
		alloc[address] = &Alloc{
			Balance: "0x200000000000000000000000000000000000000000000000000000000000000",
		}
		extraData = extraData + address
	}
	extraData = strings.ReplaceAll(fmt.Sprintf("%-236s", extraData), " ", "0")

	return &Genesis{
		Config: &GenesisConfig{
			ChainId:             2021,
			HomesteadBlock:      0,
			Eip150Block:         0,
			Eip150Hash:          "0x0000000000000000000000000000000000000000000000000000000000000000",
			Eip155Block:         0,
			Eip158Block:         0,
			ByzantiumBlock:      0,
			ConstantinopleBlock: 0,
			IstanbulBlock:       0,
			Clique: &CliqueConfig{
				Period: 0,
				Epoch:  30000,
			},
		},
		Nonce:      "0x0",
		Timestamp:  "0x60edb1c7",
		ExtraData:  extraData,
		GasLimit:   "0x47b760",
		Difficulty: "0x1",
		MixHash:    "0x0000000000000000000000000000000000000000000000000000000000000000",
		Coinbase:   "0x0000000000000000000000000000000000000000",
		Alloc:      alloc,
		Number:     "0x0",
		GasUsed:    "0x0",
		ParentHash: "0x0000000000000000000000000000000000000000000000000000000000000000",
	}
}
