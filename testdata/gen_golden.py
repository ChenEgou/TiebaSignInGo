#!/usr/bin/env python3
"""
生成 MD5 基准数据，用于验证 Go 版本与 Java 版本的签名逐位一致。

Java 版 top.srcrs.util.Encryption.enCodeMd5 的核心是:
    new BigInteger(1, md.digest()).toString(16)

语义 = 把 16 字节摘要当作无符号大端整数，输出最短的小写十六进制表示
     = 标准 hex 编码后去掉全部前导零

Python 的 format(int.from_bytes(d, 'big'), 'x') 与之完全等价。

用法: python3 gen_golden.py > md5_golden.txt
"""
import hashlib

for i in range(4000):
    kw = "测试贴吧%d" % i
    tbs = "abcdef0123456789abcdef012345678%d" % (i % 10)
    data = ("kw=" + kw + "tbs=" + tbs + "tiebaclient!!!").encode("utf-8")
    print(format(int.from_bytes(hashlib.md5(data).digest(), "big"), "x"))
