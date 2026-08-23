VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "PricingSseParser"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = False
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
Option Explicit

#If VBA7 Then
Private Declare PtrSafe Function MultiByteToWideChar Lib "kernel32" ( _
    ByVal CodePage As Long, _
    ByVal Flags As Long, _
    ByRef MultiByteText As Any, _
    ByVal MultiByteLength As Long, _
    ByVal WideText As LongPtr, _
    ByVal WideTextLength As Long) As Long
#Else
Private Declare Function MultiByteToWideChar Lib "kernel32" ( _
    ByVal CodePage As Long, _
    ByVal Flags As Long, _
    ByRef MultiByteText As Any, _
    ByVal MultiByteLength As Long, _
    ByVal WideText As Long, _
    ByVal WideTextLength As Long) As Long
#End If

Private Const CP_UTF8 As Long = 65001
Private Const MB_ERR_INVALID_CHARS As Long = &H8

Private Const MAX_BUFFER_BYTES As Long = 1048576
Private Const MAX_EVENT_BYTES As Long = 262144

Private Const ERROR_INVALID_CHUNK As String = "invalid_chunk"
Private Const ERROR_BUFFER_LIMIT As String = "buffer_limit_exceeded"
Private Const ERROR_EVENT_LIMIT As String = "event_limit_exceeded"
Private Const ERROR_INVALID_UTF8 As String = "invalid_utf8"
Private Const ERROR_INTERNAL As String = "internal_error"

Private mBuffer() As Byte
Private mBufferLength As Long
Private mLastError As String
Private mLastEventID As String
Private mReconnectDelayMilliseconds As Long
Private mFailed As Boolean
Private mAtStreamStart As Boolean

Private Sub Class_Initialize()
    mBufferLength = 0
    mLastError = vbNullString
    mLastEventID = vbNullString
    mReconnectDelayMilliseconds = -1
    mFailed = False
    mAtStreamStart = True
End Sub

Public Sub Reset()
    On Error GoTo ResetFailed

    Erase mBuffer
    mBufferLength = 0
    mLastError = vbNullString
    mLastEventID = vbNullString
    mReconnectDelayMilliseconds = -1
    mFailed = False
    mAtStreamStart = True
    Exit Sub

ResetFailed:
    SetFailure ERROR_INTERNAL
End Sub

Public Property Get LastError() As String
    LastError = mLastError
End Property

Public Property Get LastEventID() As String
    LastEventID = mLastEventID
End Property

Public Property Get ReconnectDelayMilliseconds() As Long
    ReconnectDelayMilliseconds = mReconnectDelayMilliseconds
End Property

Public Property Get BufferedByteCount() As Long
    BufferedByteCount = mBufferLength
End Property

Public Property Get HasFailed() As Boolean
    HasFailed = mFailed
End Property

Public Function Feed(ByVal Chunk As Variant) As Collection
    Dim emitted As Collection
    Dim chunkLower As Long
    Dim chunkUpper As Long
    Dim chunkLength As Long
    Dim eventStart As Long
    Dim lineStart As Long
    Dim index As Long
    Dim lineLength As Long
    Dim frameLength As Long
    Dim stagedLastEventID As String
    Dim stagedReconnectDelay As Long
    Dim stagedAtStreamStart As Boolean
    Dim parsedEvent As Object

    Set emitted = New Collection
    Set Feed = emitted

    On Error GoTo FeedFailed

    If mFailed Then Exit Function

    If VarType(Chunk) <> (vbArray Or vbByte) Then
        SetFailure ERROR_INVALID_CHUNK
        Exit Function
    End If

    If Not TryGetByteArrayBounds(Chunk, chunkLower, chunkUpper, chunkLength) Then
        SetFailure ERROR_INVALID_CHUNK
        Exit Function
    End If

    If chunkLength > 0 Then
        If chunkLength > MAX_BUFFER_BYTES - mBufferLength Then
            SetFailure ERROR_BUFFER_LIMIT
            Exit Function
        End If

        If Not AppendChunk(Chunk, chunkLower, chunkLength) Then GoTo FeedRejected
    End If

    stagedLastEventID = mLastEventID
    stagedReconnectDelay = mReconnectDelayMilliseconds
    stagedAtStreamStart = mAtStreamStart
    eventStart = 0
    lineStart = 0

    For index = 0 To mBufferLength - 1
        If mBuffer(index) = 10 Then
            lineLength = index - lineStart
            If lineLength > 0 Then
                If mBuffer(index - 1) = 13 Then lineLength = lineLength - 1
            End If

            If lineLength = 0 Then
                frameLength = lineStart - eventStart
                If frameLength > MAX_EVENT_BYTES Then
                    SetFailure ERROR_EVENT_LIMIT
                    GoTo FeedRejected
                End If

                Set parsedEvent = Nothing
                If Not ParseFrame(eventStart, _
                                  frameLength, _
                                  stagedLastEventID, _
                                  stagedReconnectDelay, _
                                  stagedAtStreamStart, _
                                  parsedEvent) Then
                    GoTo FeedRejected
                End If

                If Not parsedEvent Is Nothing Then emitted.Add parsedEvent
                eventStart = index + 1
                lineStart = index + 1
            Else
                If index - eventStart + 1 > MAX_EVENT_BYTES Then
                    SetFailure ERROR_EVENT_LIMIT
                    GoTo FeedRejected
                End If

                lineStart = index + 1
            End If
        End If
    Next index

    If mBufferLength - eventStart > MAX_EVENT_BYTES Then
        SetFailure ERROR_EVENT_LIMIT
        GoTo FeedRejected
    End If

    If Not DiscardPrefix(eventStart) Then GoTo FeedRejected

    mLastEventID = stagedLastEventID
    mReconnectDelayMilliseconds = stagedReconnectDelay
    mAtStreamStart = stagedAtStreamStart
    Exit Function

FeedRejected:
    Set emitted = New Collection
    Set Feed = emitted
    Exit Function

FeedFailed:
    SetFailure ERROR_INTERNAL
    Set emitted = New Collection
    Set Feed = emitted
End Function

Private Function AppendChunk(ByRef Chunk As Variant, _
                             ByVal ChunkLower As Long, _
                             ByVal ChunkLength As Long) As Boolean
    Dim previousLength As Long
    Dim newLength As Long
    Dim index As Long

    On Error GoTo AppendFailed

    previousLength = mBufferLength
    newLength = previousLength + ChunkLength

    If previousLength = 0 Then
        ReDim mBuffer(0 To newLength - 1)
    Else
        ReDim Preserve mBuffer(0 To newLength - 1)
    End If

    For index = 0 To ChunkLength - 1
        mBuffer(previousLength + index) = CByte(Chunk(ChunkLower + index))
    Next index

    mBufferLength = newLength
    AppendChunk = True
    Exit Function

AppendFailed:
    SetFailure ERROR_INTERNAL
End Function

Private Function ParseFrame(ByVal FrameStart As Long, _
                            ByVal FrameLength As Long, _
                            ByRef EffectiveLastEventID As String, _
                            ByRef EffectiveReconnectDelay As Long, _
                            ByRef AtStreamStart As Boolean, _
                            ByRef ParsedEvent As Object) As Boolean
    Dim frameText As String
    Dim lines As Variant
    Dim lineValue As Variant
    Dim currentLine As String
    Dim fieldName As String
    Dim fieldValue As String
    Dim colonPosition As Long
    Dim eventName As String
    Dim eventData As String
    Dim hasData As Boolean
    Dim frameHasRetry As Boolean
    Dim frameRetry As Long
    Dim retryValue As Long
    Dim eventObject As Object

    On Error GoTo ParseFailed

    Set ParsedEvent = Nothing

    If FrameLength = 0 Then
        AtStreamStart = False
        ParseFrame = True
        Exit Function
    End If

    If Not DecodeUtf8(FrameStart, FrameLength, frameText) Then Exit Function

    If AtStreamStart Then
        If Len(frameText) > 0 Then
            If AscW(Left$(frameText, 1)) = -257 Then
                frameText = Mid$(frameText, 2)
            End If
        End If
    End If
    AtStreamStart = False

    frameText = Replace$(frameText, vbCrLf, vbLf)
    lines = Split(frameText, vbLf)

    For Each lineValue In lines
        currentLine = CStr(lineValue)

        If Len(currentLine) > 0 Then
            If Left$(currentLine, 1) <> ":" Then
                colonPosition = InStr(1, currentLine, ":", vbBinaryCompare)

                If colonPosition = 0 Then
                    fieldName = currentLine
                    fieldValue = vbNullString
                Else
                    fieldName = Left$(currentLine, colonPosition - 1)
                    fieldValue = Mid$(currentLine, colonPosition + 1)
                    If Left$(fieldValue, 1) = " " Then fieldValue = Mid$(fieldValue, 2)
                End If

                Select Case fieldName
                    Case "data"
                        If hasData Then eventData = eventData & vbLf
                        eventData = eventData & fieldValue
                        hasData = True

                    Case "event"
                        eventName = fieldValue

                    Case "id"
                        If InStr(1, fieldValue, ChrW$(0), vbBinaryCompare) = 0 Then
                            EffectiveLastEventID = fieldValue
                        End If

                    Case "retry"
                        If TryParseRetry(fieldValue, retryValue) Then
                            EffectiveReconnectDelay = retryValue
                            frameRetry = retryValue
                            frameHasRetry = True
                        End If
                End Select
            End If
        End If
    Next lineValue

    If hasData Then
        If Len(eventName) = 0 Then eventName = "message"

        Set eventObject = CreateObject("Scripting.Dictionary")
        eventObject.CompareMode = vbBinaryCompare
        eventObject.Add "id", EffectiveLastEventID
        eventObject.Add "event", eventName
        eventObject.Add "data", eventData

        If frameHasRetry Then
            eventObject.Add "retry", frameRetry
        Else
            eventObject.Add "retry", Empty
        End If

        Set ParsedEvent = eventObject
    End If

    ParseFrame = True
    Exit Function

ParseFailed:
    SetFailure ERROR_INTERNAL
End Function

Private Function DecodeUtf8(ByVal ByteStart As Long, _
                            ByVal ByteLength As Long, _
                            ByRef DecodedText As String) As Boolean
    Dim requiredCharacters As Long
    Dim writtenCharacters As Long

    On Error GoTo DecodeFailed

    If ByteLength = 0 Then
        DecodedText = vbNullString
        DecodeUtf8 = True
        Exit Function
    End If

    requiredCharacters = MultiByteToWideChar(CP_UTF8, _
                                              MB_ERR_INVALID_CHARS, _
                                              mBuffer(ByteStart), _
                                              ByteLength, _
                                              0, _
                                              0)
    If requiredCharacters <= 0 Then
        SetFailure ERROR_INVALID_UTF8
        Exit Function
    End If

    DecodedText = String$(requiredCharacters, vbNullChar)
    writtenCharacters = MultiByteToWideChar(CP_UTF8, _
                                            MB_ERR_INVALID_CHARS, _
                                            mBuffer(ByteStart), _
                                            ByteLength, _
                                            StrPtr(DecodedText), _
                                            requiredCharacters)
    If writtenCharacters <> requiredCharacters Then
        SetFailure ERROR_INVALID_UTF8
        Exit Function
    End If

    DecodeUtf8 = True
    Exit Function

DecodeFailed:
    SetFailure ERROR_INTERNAL
End Function

Private Function TryParseRetry(ByVal Value As String, _
                               ByRef ParsedValue As Long) As Boolean
    Dim accumulator As Double
    Dim index As Long
    Dim characterCode As Long

    If Len(Value) = 0 Then Exit Function

    For index = 1 To Len(Value)
        characterCode = AscW(Mid$(Value, index, 1))
        If characterCode < 48 Or characterCode > 57 Then Exit Function

        accumulator = (accumulator * 10#) + (characterCode - 48)
        If accumulator > 2147483647# Then Exit Function
    Next index

    ParsedValue = CLng(accumulator)
    TryParseRetry = True
End Function

Private Function TryGetByteArrayBounds(ByRef Value As Variant, _
                                       ByRef LowerBound As Long, _
                                       ByRef UpperBound As Long, _
                                       ByRef ByteCount As Long) As Boolean
    On Error GoTo EmptyArray

    LowerBound = LBound(Value)
    UpperBound = UBound(Value)

    If UpperBound < LowerBound Then GoTo EmptyArray

    ByteCount = UpperBound - LowerBound + 1
    TryGetByteArrayBounds = True
    Exit Function

EmptyArray:
    Err.Clear
    LowerBound = 0
    UpperBound = -1
    ByteCount = 0
    TryGetByteArrayBounds = True
End Function

Private Function DiscardPrefix(ByVal ByteCount As Long) As Boolean
    Dim remaining As Long
    Dim index As Long

    On Error GoTo DiscardFailed

    If ByteCount <= 0 Then
        DiscardPrefix = True
        Exit Function
    End If

    remaining = mBufferLength - ByteCount

    If remaining <= 0 Then
        Erase mBuffer
        mBufferLength = 0
        DiscardPrefix = True
        Exit Function
    End If

    For index = 0 To remaining - 1
        mBuffer(index) = mBuffer(ByteCount + index)
    Next index

    ReDim Preserve mBuffer(0 To remaining - 1)
    mBufferLength = remaining
    DiscardPrefix = True
    Exit Function

DiscardFailed:
    SetFailure ERROR_INTERNAL
End Function

Private Sub SetFailure(ByVal ErrorCode As String)
    If Len(mLastError) = 0 Then mLastError = ErrorCode
    mFailed = True
End Sub
