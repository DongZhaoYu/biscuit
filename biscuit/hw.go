package main

import "fmt"
import "runtime"
import "unsafe"

const (
	VENDOR	int	= 0x0
	DEVICE		= 0x02
	STATUS		= 0x06
	CLASS		= 0x0b
	SUBCLASS	= 0x0a
	HEADER		= 0x0e
	BAR0		= 0x10
	BAR1		= 0x14
	BAR2		= 0x18
	BAR3		= 0x1c
	BAR4		= 0x20
)

// width is width of the register in bytes
func pci_read(tag pcitag_t, reg, width int) int {
	enable := 1 << 31
	rsh := reg % 4
	r := reg - rsh
	t := enable | int(tag) | r

	pci_addr := 0xcf8
	pci_data := 0xcfc
	runtime.Outl(pci_addr, t)
	d := runtime.Inl(pci_data)
	runtime.Outl(pci_addr, 0)

	ret := int(uint(d) >> uint(rsh*8))
	m := ((1 << (8*uint(width))) - 1)
	return ret & m
}

func pci_write(tag pcitag_t, reg, val int) {
	if reg & 3 != 0 {
		panic("reg must be 32bit aligned")
	}
	enable := 1 << 31
	t := enable | int(tag) | reg

	pci_addr := 0xcf8
	pci_data := 0xcfc
	runtime.Outl(pci_addr, t)
	runtime.Outl(pci_data, val)
	runtime.Outl(pci_addr, 0)
}

// XXX enable port IO/mem/busmaster in pci command reg before attaching
// XXX handle mem mapped bar types too
func pci_bar(tag pcitag_t, barn int) int {
	if barn < 0 || barn > 4 {
		panic("bad bar #")
	}
	ret := pci_read(tag, BAR0 + 4*barn, 4)
	m := ((1 << 16) - 1)
	m &= ^0x7
	return ret & m
}

func pci_dump() {
	pcipr := func(b, dev, f int, ind bool) (int, bool) {
		t := mkpcitag(b, dev, f)
		v  := pci_read(t, VENDOR, 2)
		if v == 0xffff {
			return 0, false
		}
		d   := pci_read(t, DEVICE, 2)
		mf  := pci_read(t, HEADER, 1)
		cl  := pci_read(t, CLASS, 1)
		scl := pci_read(t, SUBCLASS, 1)
		if ind {
			fmt.Printf("    ")
		}
		fmt.Printf("%d: %d: %d: %#x %#x (%#x %#x)\n", b, dev, f, v, d,
		    cl, scl)
		return mf, true
	}
	fmt.Printf("PCI dump:\n")
	for b := 0; b < 256; b++ {
		for dev := 0; dev < 32; dev++ {
			mf, ok := pcipr(b, dev, 0, false)
			if !ok {
				continue
			}
			if mf & 0x80 != 0 {
				for f := 1; f < 8; f++ {
					pcipr(b, dev, f, true)
				}
			}
		}
	}
}

func pcibus_attach() {
	pciinfo := func(b, dev, f int) (int, int, bool, bool) {
		t := mkpcitag(b, dev, f)
		v  := pci_read(t, VENDOR, 2)
		if v == 0xffff {
			return 0, 0, false, false
		}
		d  := pci_read(t, DEVICE, 2)
		mf := pci_read(t, HEADER, 1)
		ismf := mf & 0x80 != 0
		return v, d, ismf, true
	}
	devattach := func(b, dev int) {
		vid, did, mf, ok := pciinfo(b, dev, 0)
		if !ok {
			return
		}
		pci_attach(vid, did, b, dev, 0)
		if !mf {
			return
		}
		// attach multi functions too
		for f := 1; f < 8; f++ {
			vid, did, _, ok := pciinfo(b, dev, f)
			if !ok {
				continue
			}
			pci_attach(vid, did, b, dev, f)
		}
	}
	for b := 0; b < 3; b++ {
		for dev := 0; dev < 32; dev++ {
			devattach(b, dev)
		}
	}
}

type pcitag_t uint

func mkpcitag(b, d, f int) pcitag_t {
	return pcitag_t(b << 16 | d << 11 | f << 8)
}

func breakpcitag(b pcitag_t) (int, int, int) {
	bus := int((b >> 16) & 0xff)
	d := int((b >> 11) & 0x1f)
	f := int((b >> 8) & 0x7)
	return bus, d, f
}

func pci_attach(vendorid, devid, bus, dev, fu int) {
	PCI_VEND_INTEL := 0x8086
	PCI_DEV_PIIX3 := 0x7000
	PCI_DEV_3400  := 0x3b20

	// map from vendor ids to a map of device ids to attach functions
	alldevs := map[int]map[int]func(int, int, pcitag_t) {
		PCI_VEND_INTEL : {
			PCI_DEV_PIIX3 : attach_piix3,
			PCI_DEV_3400 : attach_3400,
			},
		}

	tag := mkpcitag(bus, dev, fu)
	devs, ok := alldevs[vendorid]
	if !ok {
		return
	}
	attach, ok := devs[devid]
	if !ok {
		return
	}
	attach(vendorid, devid, tag)
}

func attach_piix3(vendorid, devid int, tag pcitag_t) {
	if disk != nil {
		panic("adding two disks")
	}
	IRQ_DISK = 14
	INT_DISK = IRQ_BASE + IRQ_DISK

	d := &legacy_disk_t{}
	d.init(0x1f0, 0x3f6)
	disk = d
	fmt.Printf("legacy disk attached\n")
}

func attach_3400(vendorid, devid int, tag pcitag_t) {
	if disk != nil {
		panic("adding two disks")
	}

	intline := 0x3c
	irq := pci_read(tag, intline, 1)

	IRQ_DISK = irq
	INT_DISK = IRQ_BASE + IRQ_DISK

	d := &pciide_disk_t{}
	// 3400's PCI-native IDE command/control block
	rbase := pci_bar(tag, 0)
	allstats := pci_bar(tag, 1)
	busmaster := pci_bar(tag, 4)

	d.init(rbase, allstats, busmaster)
	disk = d
	fmt.Printf("3400: base %#x, cntrl: %#x, bm: %#x, irq: %d\n", rbase,
	    allstats, busmaster, irq)
}

type disk_t interface {
	start(*idebuf_t, bool)
	complete([]uint8, bool)
	intr() bool
	int_clear()
}

// use ata pio for fair comparisons against xv6, but i want to use ahci (or
// something) eventually. unlike xv6, we always use disk 0
const(
	ide_bsy = 0x80
	ide_drdy = 0x40
	ide_df = 0x20
	ide_err = 0x01

	ide_cmd_read = 0x20
	ide_cmd_write = 0x30
)

func ide_init(rbase int) bool {
	ide_wait(rbase, false)

	found := false
	for i := 0; i < 1000; i++ {
		r := int(runtime.Inb(uint16(rbase + 7)))
		if r == 0xff {
			fmt.Printf("floating bus!\n")
			break
		} else if r != 0 {
			found = true
			break
		}
	}
	if found {
		fmt.Printf("IDE disk detected\n");
		return true
	}

	fmt.Printf("no IDE disk\n");
	return false
}

func ide_wait(base int, chk bool) bool {
	var r int
	c := 0
	for {
		r = int(runtime.Inb(uint16(base + 7)))
		if r & (ide_bsy | ide_drdy) == ide_drdy {
			break
		}
		c++
		if c > 10000000 {
			fmt.Printf("waiting a very long time for disk...\n")
			c = 0
		}
	}
	if chk && r & (ide_df | ide_err) != 0 {
		return false
	}
	return true
}

func idedata_ready(base int) {
	c := 0
	for {
		drq := 1 << 3
		st := int(runtime.Inb(uint16(base + 7)))
		if st & drq != 0 {
			return
		}
		c++
		if c > 10000000 {
			fmt.Printf("waiting a long time for DRQ...\n")
		}
	}
}
// it is possible that a goroutine is context switched to a new CPU while doing
// this port io; does this matter? doesn't seem to for qemu...
func ide_start(rbase, allstatus int, ibuf *idebuf_t, writing bool) {
	ireg := func(n int) uint16 {
		return uint16(rbase + n)
	}
	ide_wait(rbase, false)
	outb := runtime.Outb
	outb(uint16(allstatus), 0)
	outb(ireg(2), 1)
	bn := ibuf.block
	bd := ibuf.disk
	outb(ireg(3), uint8(bn & 0xff))
	outb(ireg(4), uint8((bn >> 8) & 0xff))
	outb(ireg(5), uint8((bn >> 16) & 0xff))
	outb(ireg(6), uint8(0xe0 | ((bd & 1) << 4) | (bn >> 24) & 0xf))
	if writing {
		outb(ireg(7), ide_cmd_write)
		idedata_ready(rbase)
		runtime.Outsl(int(ireg(0)), unsafe.Pointer(&ibuf.data[0]),
		    512/4)
	} else {
		outb(ireg(7), ide_cmd_read)
	}
}

func ide_complete(base int, dst []uint8, writing bool) {
	if !writing {
		// read sector
		if ide_wait(base, true) {
			runtime.Insl(base + 0,
			    unsafe.Pointer(&dst[0]), 512/4)
		}
	} else {
		// cache flush; only needed for old disks?
		//runtime.Outb(base + 7, 0xe7)
	}
}

type legacy_disk_t struct {
	rbase	int
	allstat	int
}

func (d *legacy_disk_t) init(base, allst int) {
	d.rbase = base
	d.allstat = allst
	ide_init(d.rbase)
}

func (d *legacy_disk_t) start(ibuf *idebuf_t, writing bool) {
	ide_start(d.rbase, d.allstat, ibuf, writing)
}

func (d *legacy_disk_t) complete(dst []uint8, writing bool) {
	ide_complete(d.rbase, dst, writing)
}

func (d *legacy_disk_t) intr() bool {
	return true
}

func (d *legacy_disk_t) int_clear() {
	// read status so disk clears int
	runtime.Inb(uint16(d.rbase + 7))
	runtime.Inb(uint16(d.rbase + 7))
	p8259_eoi(IRQ_DISK)
}

type pciide_disk_t struct {
	rbase	int
	allstat	int
	bmaster int
}

func (d *pciide_disk_t) init(base, allst, busmaster int) {
	d.rbase = base
	d.allstat = allst
	d.bmaster = busmaster
	ide_init(d.rbase)
}

func (d *pciide_disk_t) start(ibuf *idebuf_t, writing bool) {
	ide_start(d.rbase, d.allstat, ibuf, writing)
}

func (d *pciide_disk_t) complete(dst []uint8, writing bool) {
	ide_complete(d.rbase, dst, writing)
}

func (d *pciide_disk_t) intr() bool {
	streg := uint16(d.bmaster + 0x02)
	bmintr := uint(1 << 2)
	st := runtime.Inb(streg)
	if st & bmintr == 0 {
		return false
	}
	return true
}

func (d *pciide_disk_t) int_clear() {
	// read status so disk clears int
	runtime.Inb(uint16(d.rbase + 7))
	runtime.Inb(uint16(d.rbase + 7))

	// in PCI-native mode, clear the interrupt via the legacy bus master
	// base, bar 4.
	streg := uint16(d.bmaster + 0x02)
	st := runtime.Inb(streg)
	er := uint(1 << 1)
	if st & er != 0 {
		panic("disk error")
	}
	runtime.Outb(streg, uint8(st))

	// and via 8259 pics
	p8259_eoi(IRQ_DISK)
}

func _acpi_cpu_count(rsdt []uint8) (int, bool) {
	// find MADT table. RSDT contains 32bit pointers, XSDT contains 64bit
	// pointers.
	hdrlen := 36
	ptrs := rsdt[hdrlen:]
	var tbl []uint8
	found := false
	for len(ptrs) != 0 {
		tbln := readn(ptrs, 4, 0)
		ptrs = ptrs[4:]
		tbl = dmaplen(tbln, 8)
		if string(tbl[:4]) == "APIC" {
			found = true
			l := readn(tbl, 4, 4)
			tbl = dmaplen(tbln, l)
			break
		}
	}
	if !found {
		return 0, false
	}
	var cksum uint8
	for _, c := range tbl {
		cksum += c
	}
	if cksum != 0 {
		fmt.Printf("MADT checksum fail\n")
		return 0, false
	}
	marrayoff := 44
	ncpu := 0
	nioapic := 0
	elen := 1
	// m is array of "interrupt controller structures" in MADT
	for m := tbl[marrayoff:]; len(m) != 0; m = m[m[elen]:] {
		// ACPI 5.2.12.2: each processor is required to have a LAPIC
		// entry
		tlapic  := uint8(0)
		tioapic := uint8(1)
		if m[0] == tlapic {
			flags := readn(m, 4, 4)
			enabled := 1
			if flags & enabled != 0 {
				ncpu++
			}
		}
		if m[0] == tioapic {
			nioapic++
			fmt.Printf("IO APIC addr: %x\n", readn(m, 4, 4))
		}
	}
	return ncpu, ncpu != 0
}

func _acpi_scan() ([]uint8, bool) {
	// ACPI 5.2.5: search for RSDP in EBDA and BIOS read-only memory
	ebdap := (0x40 << 4) | 0xe
	p := dmap8(ebdap)
	ebda := readn(p, 2, 0)
	ebda <<= 4

	isrsdp := func(d []uint8) bool {
		s := string(d[:8])
		if s != "RSD PTR " {
			return false
		}
		var cksum uint8
		for i := 0; i < 20; i++ {
			cksum += d[i]
		}
		if cksum != 0 {
			return false
		}
		return true
	}
	rsdplen := 36
	for i := 0; i < 1 << 10; i += 16 {
		p = dmaplen(ebda + i, rsdplen)
		if isrsdp(p) {
			return p, true
		}
	}
	for bmem := 0xe0000; bmem < 0xfffff; bmem += 16 {
		p = dmaplen(bmem, rsdplen)
		if isrsdp(p) {
			return p, true
		}
	}
	return nil, false
}

func acpi_init() int {
	rsdp, ok := _acpi_scan()
	if !ok {
		panic("no RSDP")
	}
	rsdtn := readn(rsdp, 4, 16)
	//xsdtn := readn(rsdp, 8, 24)
	rsdt := dmaplen(rsdtn, 8)
	if rsdtn == 0 || string(rsdt[:4]) != "RSDT" {
		panic("no RSDT")
	}
	rsdtlen := readn(rsdt, 4, 4)
	rsdt = dmaplen(rsdtn, rsdtlen)
	// verify RSDT checksum
	var cksum uint8
	for i := 0; i < rsdtlen; i++ {
		cksum += rsdt[i]
	}
	if cksum != 0 {
		panic("bad RSDT")
	}
	ncpu, ok := _acpi_cpu_count(rsdt)
	if !ok {
		panic("no cpu count")
	}
	return ncpu
}
