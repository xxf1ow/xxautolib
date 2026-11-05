cmake_minimum_required(VERSION 3.20)

file(READ "${PC_FILE}" contents)
string(REGEX REPLACE "-lportaudio([ \t\r\n]|$)" "-lportaudio_x64\\1" contents "${contents}")
file(WRITE "${PC_FILE}" "${contents}")
