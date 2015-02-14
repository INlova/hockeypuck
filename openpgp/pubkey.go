/*
   Hockeypuck - OpenPGP key server
   Copyright (C) 2012-2014  Casey Marshall

   This program is free software: you can redistribute it and/or modify
   it under the terms of the GNU Affero General Public License as published by
   the Free Software Foundation, version 3.

   This program is distributed in the hope that it will be useful,
   but WITHOUT ANY WARRANTY; without even the implied warranty of
   MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
   GNU Affero General Public License for more details.

   You should have received a copy of the GNU Affero General Public License
   along with this program.  If not, see <http://www.gnu.org/licenses/>.
*/

package openpgp

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"golang.org/x/crypto/openpgp/packet"
	"gopkg.in/errgo.v1"
)

type publicKeyPacket struct {
	Packet

	ShortID string
	KeyID   string

	// Creation stores the timestamp when the public key was created.
	Creation time.Time

	// Expiration stores the timestamp when the public key expires.
	Expiration time.Time

	// Algorithm stores the algorithm type of the public key.
	Algorithm int

	// BitLen stores the bit length of the public key.
	BitLen int

	Signatures []*Signature
	Others     []*Packet
}

// appendSignature implements signable.
func (pk *publicKeyPacket) appendSignature(sig *Signature) {
	pk.Signatures = append(pk.Signatures, sig)
}

func (pkp *publicKeyPacket) publicKeyPacket() (*packet.PublicKey, error) {
	op, err := pkp.opaquePacket()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	p, err := op.Parse()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	pk, ok := p.(*packet.PublicKey)
	if !ok {
		return nil, errgo.Newf("expected public key packet, got %T", p)
	}
	return pk, nil
}

func (pkp *publicKeyPacket) publicKeyV3Packet() (*packet.PublicKeyV3, error) {
	op, err := pkp.opaquePacket()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	p, err := op.Parse()
	if err != nil {
		return nil, errgo.Mask(err)
	}
	pk, ok := p.(*packet.PublicKeyV3)
	if !ok {
		return nil, errgo.Newf("expected public key V3 packet, got %T", p)
	}
	return pk, nil
}

func (pkp *publicKeyPacket) parse(op *packet.OpaquePacket, subkey bool) error {
	p, err := op.Parse()
	if err != nil {
		return errgo.Mask(err)
	}

	switch pk := p.(type) {
	case *packet.PublicKey:
		if pk.IsSubkey != subkey {
			return ErrInvalidPacketType
		}
		return pkp.setPublicKey(pk)
	case *packet.PublicKeyV3:
		if pk.IsSubkey != subkey {
			return ErrInvalidPacketType
		}
		return pkp.setPublicKeyV3(pk)
	default:
	}

	return errgo.Mask(ErrInvalidPacketType)
}

func (pkp *publicKeyPacket) setUnsupported(op *packet.OpaquePacket) error {
	// Calculate opaque fingerprint on unsupported public key packet
	h := sha1.New()
	h.Write([]byte{0x99, byte(len(op.Contents) >> 8), byte(len(op.Contents))})
	h.Write(op.Contents)
	fpr := hex.EncodeToString(h.Sum(nil))
	pkp.UUID = reverse(fpr)
	return pkp.setV4IDs(pkp.UUID)
}

func (pkp *publicKeyPacket) setPublicKey(pk *packet.PublicKey) error {
	buf := bytes.NewBuffer(nil)
	err := pk.Serialize(buf)
	if err != nil {
		return errgo.Mask(err)
	}
	fingerprint := hex.EncodeToString(pk.Fingerprint[:])
	bitLen, err := pk.BitLength()
	if err != nil {
		return errgo.Mask(err)
	}
	pkp.UUID = reverse(fingerprint)
	err = pkp.setV4IDs(pkp.UUID)
	if err != nil {
		return err
	}
	pkp.Creation = pk.CreationTime
	pkp.Expiration = NeverExpires
	pkp.Algorithm = int(pk.PubKeyAlgo)
	pkp.BitLen = int(bitLen)
	pkp.Valid = true
	return nil
}

func (pkp *publicKeyPacket) setV4IDs(uuid string) error {
	var ok bool
	pkp.ShortID, ok = suffixID(uuid, 8)
	if !ok {
		return errgo.Newf("invalid fingerprint %q", uuid)
	}
	pkp.KeyID, ok = suffixID(uuid, 16)
	if !ok {
		return errgo.Newf("invalid fingerprint %q", uuid)
	}
	return nil
}

func (pkp *publicKeyPacket) setPublicKeyV3(pk *packet.PublicKeyV3) error {
	var buf bytes.Buffer
	err := pk.Serialize(&buf)
	if err != nil {
		return errgo.Mask(err)
	}
	fingerprint := hex.EncodeToString(pk.Fingerprint[:])
	bitLen, err := pk.BitLength()
	if err != nil {
		return errgo.Mask(err)
	}
	pkp.UUID = reverse(fingerprint)
	pkp.ShortID = fmt.Sprintf("%08x", uint32(pk.KeyId))
	pkp.KeyID = fmt.Sprintf("%016x", pk.KeyId)
	pkp.Creation = pk.CreationTime
	pkp.Expiration = NeverExpires
	if pk.DaysToExpire > 0 {
		pkp.Expiration = pkp.Creation.Add(time.Duration(pk.DaysToExpire) * time.Hour * 24)
	}
	pkp.Algorithm = int(pk.PubKeyAlgo)
	pkp.BitLen = int(bitLen)
	pkp.Valid = true
	return nil
}

func reverse(s string) string {
	runes := []rune(s)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	return string(runes)
}

func suffixID(rid string, n int) (string, bool) {
	l := len(rid)
	if l < n {
		return "", false
	}
	id := reverse(rid)
	return id[l-n : l], true
}

type Pubkey struct {
	publicKeyPacket

	Subkeys        []*Subkey
	UserIDs        []*UserID
	UserAttributes []*UserAttribute
}

// contents implements the packetNode interface for top-level public keys.
func (pubkey *Pubkey) contents() []packetNode {
	result := []packetNode{pubkey}
	for _, sig := range pubkey.Signatures {
		result = append(result, sig.contents()...)
	}
	for _, uid := range pubkey.UserIDs {
		result = append(result, uid.contents()...)
	}
	for _, uat := range pubkey.UserAttributes {
		result = append(result, uat.contents()...)
	}
	for _, subkey := range pubkey.Subkeys {
		result = append(result, subkey.contents()...)
	}
	for _, other := range pubkey.Others {
		result = append(result, other.contents()...)
	}
	return result
}

func (*Pubkey) removeDuplicate(parent packetNode, dup packetNode) error {
	return errgo.New("cannot remove a duplicate primary pubkey")
}

func ParsePubkey(op *packet.OpaquePacket) (*Pubkey, error) {
	var buf bytes.Buffer
	var err error

	if err = op.Serialize(&buf); err != nil {
		return nil, errgo.Mask(err)
	}
	pubkey := &Pubkey{
		publicKeyPacket: publicKeyPacket{
			Packet: Packet{
				Tag:    op.Tag,
				Packet: buf.Bytes(),
			},
		},
	}

	// Attempt to parse the opaque packet into a public key type.
	parseErr := pubkey.parse(op, false)
	if parseErr != nil {
		err = pubkey.setUnsupported(op)
		if err != nil {
			return nil, errgo.Mask(err)
		}
	} else {
		pubkey.Valid = true
	}

	return pubkey, nil
}

func (pubkey *Pubkey) setPublicKey(pk *packet.PublicKey) error {
	if pk.IsSubkey {
		return errgo.NoteMask(ErrInvalidPacketType, "expected primary public key packet, got sub-key")
	}
	return pubkey.publicKeyPacket.setPublicKey(pk)
}

func (pubkey *Pubkey) setPublicKeyV3(pk *packet.PublicKeyV3) error {
	if pk.IsSubkey {
		return errgo.NoteMask(ErrInvalidPacketType, "expected primary public key packet, got sub-key")
	}
	return pubkey.publicKeyPacket.setPublicKeyV3(pk)
}

func (pubkey *Pubkey) SelfSigs() *SelfSigs {
	result := &SelfSigs{}
	for _, sig := range pubkey.Signatures {
		// Skip non-self-certifications.
		if !strings.HasPrefix(pubkey.UUID, sig.RIssuerKeyID) {
			continue
		}
		switch sig.SigType {
		case 0x20: // packet.SigTypeKeyRevocation
			result.Revocations = append(result.Revocations, &CheckSig{
				Pubkey:    pubkey,
				Signature: sig,
				Error:     pubkey.verifyPublicKeySelfSig(&pubkey.publicKeyPacket, sig),
				target:    pubkey,
			})
		}
	}
	return result
}
