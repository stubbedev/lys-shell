package pipewire

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/unix"
)

// idInvalid is SPA_ID_INVALID: no object, no memory, no buffer.
const idInvalid = 0xffffffff

// Memblock flags (pw_memblock_flags) the server marks a block with.
const (
	memFlagReadable = 1 << 0
	memFlagWritable = 1 << 1
)

// memBlock is one shared memory block the server announced (core
// add_mem), referenced by id from the client-node events.
type memBlock struct {
	fd    int
	flags uint32
}

// memPool holds the announced blocks by id; the server allocates ids
// densely from 0.
type memPool struct {
	blocks []memBlock
}

// maxMemBlocks bounds the ids the pool grows to.
const maxMemBlocks = 1 << 16

func (p *memPool) add(id uint32, fd int, flags uint32) error {
	if id >= maxMemBlocks {
		_ = unix.Close(fd)
		return fmt.Errorf("pipewire: memory id %d exceeds %d", id, maxMemBlocks)
	}
	for uint32(len(p.blocks)) <= id {
		p.blocks = append(p.blocks, memBlock{fd: -1})
	}
	if p.blocks[id].fd >= 0 {
		_ = unix.Close(fd)
		return fmt.Errorf("pipewire: memory id %d announced twice", id)
	}
	p.blocks[id] = memBlock{fd: fd, flags: flags}
	return nil
}

// remove closes a block's fd; its mappings stay valid until unmapped.
func (p *memPool) remove(id uint32) error {
	if id >= uint32(len(p.blocks)) || p.blocks[id].fd < 0 {
		return fmt.Errorf("pipewire: removing unknown memory id %d", id)
	}
	_ = unix.Close(p.blocks[id].fd)
	p.blocks[id] = memBlock{fd: -1}
	return nil
}

// mapID maps size bytes at offset of block id read-write.
func (p *memPool) mapID(id, offset, size uint32) (mapping, error) {
	if id >= uint32(len(p.blocks)) || p.blocks[id].fd < 0 {
		return mapping{}, fmt.Errorf("pipewire: map unknown memory id %d", id)
	}
	b := p.blocks[id]
	if b.flags&(memFlagReadable|memFlagWritable) != memFlagReadable|memFlagWritable {
		return mapping{}, fmt.Errorf("pipewire: memory id %d is not read-write (flags %#x)", id, b.flags)
	}
	m, err := mapFd(b.fd, offset, size)
	if err != nil {
		return mapping{}, fmt.Errorf("pipewire: map memory id %d: %w", id, err)
	}
	return m, nil
}

func (p *memPool) close() {
	for _, b := range p.blocks {
		if b.fd >= 0 {
			_ = unix.Close(b.fd)
		}
	}
	p.blocks = nil
}

// mapping is one shared mapping: region as mmap returned it (page
// aligned), data the bytes asked for inside it.
type mapping struct {
	region []byte
	data   []byte
}

var pageSize = uint32(unix.Getpagesize())

// mapFd maps size bytes of fd at offset, which need not be page
// aligned.
func mapFd(fd int, offset, size uint32) (mapping, error) {
	if size == 0 {
		return mapping{}, fmt.Errorf("empty mapping at %d of fd %d", offset, fd)
	}
	start := offset &^ (pageSize - 1)
	skip := offset - start
	region, err := unix.Mmap(fd, int64(start), int(skip+size), unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if err != nil {
		return mapping{}, fmt.Errorf("mmap %d bytes at %d of fd %d: %w", size, offset, fd, err)
	}
	return mapping{region: region, data: region[skip : skip+size]}, nil
}

// unmap releases the mapping; the zero mapping is none.
func (m *mapping) unmap() {
	if m.region != nil {
		_ = unix.Munmap(m.region)
	}
	*m = mapping{}
}

// view is a T laid over off in mem, which must hold it and align it.
func view[T any](mem []byte, off uint32) (*T, error) {
	var zero T
	size, align := uint32(unsafe.Sizeof(zero)), unsafe.Alignof(zero)
	if uint64(off)+uint64(size) > uint64(len(mem)) {
		return nil, fmt.Errorf("%d bytes at %d of a %d-byte area", size, off, len(mem))
	}
	p := unsafe.Pointer(&mem[off]) //nolint:gosec
	if uintptr(p)%align != 0 {
		return nil, fmt.Errorf("offset %d is not %d-aligned", off, align)
	}
	return (*T)(p), nil
}

// viewSlice is n Ts laid over off in mem.
func viewSlice[T any](mem []byte, off, n uint32) ([]T, error) {
	if n == 0 {
		return nil, nil
	}
	first, err := view[T](mem, off)
	if err != nil {
		return nil, err
	}
	var zero T
	if uint64(off)+uint64(n)*uint64(unsafe.Sizeof(zero)) > uint64(len(mem)) {
		return nil, fmt.Errorf("%d items of %d bytes at %d of a %d-byte area", n, unsafe.Sizeof(zero), off, len(mem))
	}
	return unsafe.Slice(first, n), nil //nolint:gosec
}
