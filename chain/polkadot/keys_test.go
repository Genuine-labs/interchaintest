package polkadot_test

import (
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	p2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/ComposableFi/go-substrate-rpc-client/v4/signature"
	"github.com/strangelove-ventures/interchaintest/v7/chain/polkadot"
	"github.com/stretchr/testify/require"
)

func TestNodeKeyPeerID(t *testing.T) {
	nodeKey, err := hex.DecodeString("1b57e31ddf03e39c58207dfcb5445958924b818c08c303a91838e68cfac551b2")
	require.NoError(t, err, "error decoding node key from hex string")

	privKeyEd25519 := ed25519.NewKeyFromSeed(nodeKey)
	privKey, _, err := p2pCrypto.KeyPairFromStdKey(&privKeyEd25519)
	require.NoError(t, err, "error getting private key")

	id, err := peer.IDFromPrivateKey(privKey)
	require.NoError(t, err, "error getting peer id from private key")
	peerId := id.String()
	require.Equal(t, "12D3KooWCqDbuUHRNWPAuHpVnzZGCkkMwgEx7Xd6xgszqtVpH56c", peerId)
}

func Test_DeriveEd25519FromName(t *testing.T) {
	privKey, err := polkadot.DeriveEd25519FromName("Alice")
	require.NoError(t, err, "error deriving ed25519 private key")

	pubKey, err := privKey.GetPublic().Raw()
	require.NoError(t, err, "error fetching pubkey bytes")

	pubKeyEncoded, err := polkadot.EncodeAddressSS58(pubKey)
	require.NoError(t, err, "error encoding ed25519 public key to ss58")

	require.Equal(t, "5wfmbM1KN4DCJeTP6jj9TqCAKKNApYNCG4zhwcweWhXZRo1j", pubKeyEncoded)
}

func Test_DeriveSr25519FromNameAccount(t *testing.T) {
	privKeyAccount, err := polkadot.DeriveSr25519FromName([]string{"Alice"})
	require.NoError(t, err, "error deriving account sr25519 private key")

	b := privKeyAccount.Public().Encode()
	pubKeyAccount := b[:]

	pubKeyEncoded, err := polkadot.EncodeAddressSS58(pubKeyAccount)
	require.NoError(t, err, "error encoding account public key to ss58")

	kp, err := signature.KeyringPairFromSecret("//Alice", polkadot.Ss58Format)
	require.NoError(t, err, "error signature KeyringPairFromSecret")

	pubKeyDecoded, err := polkadot.DecodeAddressSS58(pubKeyEncoded)
	require.NoError(t, err, "error decoding SS58 address to pub key")

	require.Equal(t, pubKeyDecoded, kp.PublicKey)
}

func Test_DeriveSr25519FromNameStash(t *testing.T) {
	privKeyStash, err := polkadot.DeriveSr25519FromName([]string{"Alice", "stash"})
	require.NoError(t, err, "error deriving stash sr25519 private key")

	pubKeyStash := make([]byte, 32)
	for i, mkByte := range privKeyStash.Public().Encode() {
		pubKeyStash[i] = mkByte
	}
	pubKeyEncoded, err := polkadot.EncodeAddressSS58(pubKeyStash)
	require.NoError(t, err, "error encoding stash public key to ss58")

	kp, err := signature.KeyringPairFromSecret("//Alice//stash", polkadot.Ss58Format)
	require.NoError(t, err, "error signature KeyringPairFromSecret")

	require.Equal(t, kp.Address, pubKeyEncoded)
}

func Test_DeriveSecp256k1FromName(t *testing.T) {
	privKey, err := polkadot.DeriveSecp256k1FromName("Alice")
	require.NoError(t, err, "error deriving secp256k1 private key")

	pubKey := []byte{}
	y := privKey.PublicKey.Y.Bytes()
	if y[len(y)-1]%2 == 0 {
		pubKey = append(pubKey, 0x02)
	} else {
		pubKey = append(pubKey, 0x03)
	}
	pubKey = append(pubKey, privKey.PublicKey.X.Bytes()...)

	pubKeyEncoded, err := polkadot.EncodeAddressSS58(pubKey)
	require.NoError(t, err, "error encoding secp256k1 public key to ss58")

	require.Equal(t, "NaqsuM2ZDssHFdr7HU8znFsHKpgkCyrCW6aPiLpLTa8Vxi3Q9", pubKeyEncoded)
}
