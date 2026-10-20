#include <queue/atomicops.h>
#include <queue/readerwriterqueue.h>
#include <queue/readerwritercircularbuffer.h>
#include <queue/concurrentqueue.h>
#include <queue/lightweightsemaphore.h>
#include <queue/blockingconcurrentqueue.h>

int main()
{
    moodycamel::ReaderWriterQueue<int> spsc;
    moodycamel::ConcurrentQueue<int> mpmc;
    int value = 0;
    if (!spsc.enqueue(17) || !spsc.try_dequeue(value) || value != 17)
        return 1;
    if (!mpmc.enqueue(29) || !mpmc.try_dequeue(value) || value != 29)
        return 2;
    return 0;
}
