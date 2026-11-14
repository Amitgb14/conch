//go:build darwin && cgo

package sysstat

/*
#include <mach/mach.h>
#include <mach/processor_info.h>
#include <mach/mach_host.h>

// cpu_ticks fills ticks with each core's user, system, idle and nice
// ticks, four to a core, and says how many cores; -1 when it cannot.
static int cpu_ticks(unsigned int *ticks, int max) {
	natural_t n = 0;
	processor_info_array_t info;
	mach_msg_type_number_t count;
	if (host_processor_info(mach_host_self(), PROCESSOR_CPU_LOAD_INFO, &n, &info, &count) != KERN_SUCCESS) {
		return -1;
	}
	int cores = (int)n < max ? (int)n : max;
	for (int i = 0; i < cores; i++) {
		ticks[4*i+0] = info[CPU_STATE_MAX*i + CPU_STATE_USER];
		ticks[4*i+1] = info[CPU_STATE_MAX*i + CPU_STATE_SYSTEM];
		ticks[4*i+2] = info[CPU_STATE_MAX*i + CPU_STATE_IDLE];
		ticks[4*i+3] = info[CPU_STATE_MAX*i + CPU_STATE_NICE];
	}
	vm_deallocate(mach_task_self(), (vm_address_t)info, count * sizeof(integer_t));
	return cores;
}
*/
import "C"

const maxCores = 512

func darwinTicks() (CPUTicks, bool) {
	var buf [4 * maxCores]C.uint
	n := int(C.cpu_ticks(&buf[0], maxCores))
	if n <= 0 {
		return CPUTicks{}, false
	}
	raw := make([]uint32, 4*n)
	for i := range raw {
		raw[i] = uint32(buf[i])
	}
	return machTicks(raw), true
}
