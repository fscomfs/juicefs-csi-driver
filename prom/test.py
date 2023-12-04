import os
import time

def get_file_list(directory):
    file_list = []
    for root, dirs, files in os.walk(directory):
        for file in files:
            file_list.append(os.path.join(root, file))
    return file_list

def simulate_file_read(file_path, buffer_size=1024):
    # 模拟文件逐块读取操作
    with open(file_path, 'rb') as file:
        while True:
            content = file.read(buffer_size)
            if not content:
                break
            # 模拟对每块内容的处理，这里只是简单地读取每一块
            pass

def main(directory):
    file_list = get_file_list(directory)

    print(f"Total files found: {len(file_list)}")

    start_time = time.time()
    total_bytes_read = 0

    for file_path in file_list:
        simulate_file_read(file_path)
        total_bytes_read += os.path.getsize(file_path)

        # 计算并显示当前读取速度
        elapsed_time = time.time() - start_time
        current_speed = total_bytes_read / (1024 * 1024) / elapsed_time  # MB/s
        print(f"Current speed: {current_speed:.2f} MB/s", end='\r')

    end_time = time.time()
    elapsed_time = end_time - start_time
    total_speed = total_bytes_read / (1024 * 1024) / elapsed_time  # MB/s

    print(f"\nTest completed in {elapsed_time:.2f} seconds.")
    print(f"Average speed: {total_speed:.2f} MB/s")

if __name__ == "__main__":
    directory_path = input("Enter the directory path to test: ")
    main(directory_path)
