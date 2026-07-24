Attribute VB_Name = "JsonRuntime"
Option Explicit

Private Const JSON_MAX_DEPTH As Long = 64

Public Function ParseJson(ByVal jsonText As String) As JsonValue
    Dim position As Long
    Dim parsed As JsonValue

    jsonText = Replace$(jsonText, ChrW$(&HFEFF), vbNullString)
    position = 1
    Set parsed = ParseValue(jsonText, position, 0)
    SkipWhitespace jsonText, position
    If position <= Len(jsonText) Then
        Err.Raise vbObjectError + 801, "JsonRuntime.ParseJson", _
                  "Unexpected JSON content at character " & CStr(position) & "."
    End If
    Set ParseJson = parsed
End Function

Public Function JsonMember(ByVal objectValue As JsonValue, ByVal memberName As String) As JsonValue
    If objectValue Is Nothing Then Exit Function
    If objectValue.Kind <> "object" Then Exit Function
    If objectValue.ObjectItems.Exists(memberName) Then
        Set JsonMember = objectValue.ObjectItems(memberName)
    End If
End Function

Public Function JsonScalar(ByVal value As JsonValue) As Variant
    If value Is Nothing Then
        JsonScalar = Empty
    ElseIf value.Kind = "null" Or value.Kind = "object" Or value.Kind = "array" Then
        JsonScalar = Empty
    Else
        JsonScalar = value.Scalar
    End If
End Function

Public Function JsonText(ByVal objectValue As JsonValue, ByVal memberName As String) As Variant
    Dim member As JsonValue
    Set member = JsonMember(objectValue, memberName)
    JsonText = JsonScalar(member)
End Function

Public Function JsonArrayCount(ByVal arrayValue As JsonValue) As Long
    If arrayValue Is Nothing Then Exit Function
    If arrayValue.Kind <> "array" Then Exit Function
    JsonArrayCount = arrayValue.ArrayItems.Count
End Function

Public Function JsonArrayItem(ByVal arrayValue As JsonValue, ByVal index As Long) As JsonValue
    If arrayValue Is Nothing Then Exit Function
    If arrayValue.Kind <> "array" Then Exit Function
    If index < 1 Or index > arrayValue.ArrayItems.Count Then Exit Function
    Set JsonArrayItem = arrayValue.ArrayItems(index)
End Function

Private Function ParseValue(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As JsonValue
    Dim character As String
    Dim value As New JsonValue

    If depth > JSON_MAX_DEPTH Then
        Err.Raise vbObjectError + 802, "JsonRuntime.ParseJson", "JSON nesting is too deep."
    End If
    SkipWhitespace jsonText, position
    If position > Len(jsonText) Then
        Err.Raise vbObjectError + 803, "JsonRuntime.ParseJson", "Unexpected end of JSON."
    End If

    character = Mid$(jsonText, position, 1)
    Select Case character
        Case "{"
            Set ParseValue = ParseObject(jsonText, position, depth + 1)
        Case "["
            Set ParseValue = ParseArray(jsonText, position, depth + 1)
        Case """"
            value.Kind = "string"
            value.Scalar = ParseString(jsonText, position)
            Set ParseValue = value
        Case "t"
            ExpectLiteral jsonText, position, "true"
            value.Kind = "boolean"
            value.Scalar = True
            Set ParseValue = value
        Case "f"
            ExpectLiteral jsonText, position, "false"
            value.Kind = "boolean"
            value.Scalar = False
            Set ParseValue = value
        Case "n"
            ExpectLiteral jsonText, position, "null"
            value.Kind = "null"
            value.Scalar = Empty
            Set ParseValue = value
        Case Else
            If character = "-" Or IsDigit(character) Then
                value.Kind = "number"
                value.Scalar = ParseNumber(jsonText, position)
                Set ParseValue = value
            Else
                Err.Raise vbObjectError + 804, "JsonRuntime.ParseJson", _
                          "Unexpected JSON token at character " & CStr(position) & "."
            End If
    End Select
End Function

Private Function ParseObject(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As JsonValue
    Dim result As New JsonValue
    Dim key As String
    Dim child As JsonValue

    result.Kind = "object"
    Set result.ObjectItems = CreateObject("Scripting.Dictionary")
    result.ObjectItems.CompareMode = vbBinaryCompare
    position = position + 1
    SkipWhitespace jsonText, position
    If CharacterAt(jsonText, position) = "}" Then
        position = position + 1
        Set ParseObject = result
        Exit Function
    End If

    Do
        If CharacterAt(jsonText, position) <> """" Then
            RaiseExpected "object member name", position
        End If
        key = ParseString(jsonText, position)
        If result.ObjectItems.Exists(key) Then
            Err.Raise vbObjectError + 805, "JsonRuntime.ParseJson", _
                      "Duplicate JSON object member """ & key & """."
        End If
        SkipWhitespace jsonText, position
        If CharacterAt(jsonText, position) <> ":" Then RaiseExpected ":", position
        position = position + 1
        Set child = ParseValue(jsonText, position, depth)
        result.ObjectItems.Add key, child
        SkipWhitespace jsonText, position
        Select Case CharacterAt(jsonText, position)
            Case "}"
                position = position + 1
                Set ParseObject = result
                Exit Function
            Case ","
                position = position + 1
                SkipWhitespace jsonText, position
            Case Else
                RaiseExpected "comma or closing brace", position
        End Select
    Loop
End Function

Private Function ParseArray(ByRef jsonText As String, ByRef position As Long, ByVal depth As Long) As JsonValue
    Dim result As New JsonValue
    Dim child As JsonValue

    result.Kind = "array"
    Set result.ArrayItems = New Collection
    position = position + 1
    SkipWhitespace jsonText, position
    If CharacterAt(jsonText, position) = "]" Then
        position = position + 1
        Set ParseArray = result
        Exit Function
    End If

    Do
        Set child = ParseValue(jsonText, position, depth)
        result.ArrayItems.Add child
        SkipWhitespace jsonText, position
        Select Case CharacterAt(jsonText, position)
            Case "]"
                position = position + 1
                Set ParseArray = result
                Exit Function
            Case ","
                position = position + 1
                SkipWhitespace jsonText, position
            Case Else
                RaiseExpected "comma or closing bracket", position
        End Select
    Loop
End Function

Private Function ParseString(ByRef jsonText As String, ByRef position As Long) As String
    Dim character As String
    Dim escaped As String
    Dim result As String
    Dim codeUnit As Long

    If CharacterAt(jsonText, position) <> """" Then RaiseExpected "string", position
    position = position + 1
    Do While position <= Len(jsonText)
        character = Mid$(jsonText, position, 1)
        position = position + 1
        If character = """" Then
            ParseString = result
            Exit Function
        End If
        If (CLng(AscW(character)) And &HFFFF&) < 32 Then
            Err.Raise vbObjectError + 806, "JsonRuntime.ParseJson", "Control character in JSON string."
        End If
        If character <> "\" Then
            result = result & character
        Else
            If position > Len(jsonText) Then RaiseExpected "escape sequence", position
            escaped = Mid$(jsonText, position, 1)
            position = position + 1
            Select Case escaped
                Case """", "\", "/"
                    result = result & escaped
                Case "b"
                    result = result & ChrW$(&H8)
                Case "f"
                    result = result & ChrW$(&HC)
                Case "n"
                    result = result & vbLf
                Case "r"
                    result = result & vbCr
                Case "t"
                    result = result & vbTab
                Case "u"
                    codeUnit = ParseHexCodeUnit(jsonText, position)
                    result = result & CodeUnitCharacter(codeUnit)
                Case Else
                    Err.Raise vbObjectError + 807, "JsonRuntime.ParseJson", _
                              "Invalid JSON escape sequence at character " & CStr(position - 1) & "."
            End Select
        End If
    Loop
    RaiseExpected "closing quote", position
End Function

Private Function ParseHexCodeUnit(ByRef jsonText As String, ByRef position As Long) As Long
    Dim token As String
    Dim index As Long

    If position + 3 > Len(jsonText) Then RaiseExpected "four hexadecimal digits", position
    token = Mid$(jsonText, position, 4)
    For index = 1 To 4
        If InStr(1, "0123456789abcdefABCDEF", Mid$(token, index, 1), vbBinaryCompare) = 0 Then
            RaiseExpected "four hexadecimal digits", position
        End If
    Next index
    ParseHexCodeUnit = CLng("&H" & token)
    position = position + 4
End Function

Private Function CodeUnitCharacter(ByVal codeUnit As Long) As String
    If codeUnit > 32767 Then
        CodeUnitCharacter = ChrW$(codeUnit - 65536)
    Else
        CodeUnitCharacter = ChrW$(codeUnit)
    End If
End Function

Private Function ParseNumber(ByRef jsonText As String, ByRef position As Long) As String
    Dim startPosition As Long

    startPosition = position
    If CharacterAt(jsonText, position) = "-" Then position = position + 1
    If CharacterAt(jsonText, position) = "0" Then
        position = position + 1
        If IsDigit(CharacterAt(jsonText, position)) Then RaiseExpected "number delimiter", position
    ElseIf IsDigitOneToNine(CharacterAt(jsonText, position)) Then
        Do While IsDigit(CharacterAt(jsonText, position))
            position = position + 1
        Loop
    Else
        RaiseExpected "number", position
    End If

    If CharacterAt(jsonText, position) = "." Then
        position = position + 1
        If Not IsDigit(CharacterAt(jsonText, position)) Then RaiseExpected "fraction digit", position
        Do While IsDigit(CharacterAt(jsonText, position))
            position = position + 1
        Loop
    End If
    If LCase$(CharacterAt(jsonText, position)) = "e" Then
        position = position + 1
        If CharacterAt(jsonText, position) = "+" Or CharacterAt(jsonText, position) = "-" Then
            position = position + 1
        End If
        If Not IsDigit(CharacterAt(jsonText, position)) Then RaiseExpected "exponent digit", position
        Do While IsDigit(CharacterAt(jsonText, position))
            position = position + 1
        Loop
    End If
    ParseNumber = Mid$(jsonText, startPosition, position - startPosition)
End Function

Private Sub ExpectLiteral(ByRef jsonText As String, ByRef position As Long, ByVal literal As String)
    If Mid$(jsonText, position, Len(literal)) <> literal Then RaiseExpected literal, position
    position = position + Len(literal)
End Sub

Private Sub SkipWhitespace(ByRef jsonText As String, ByRef position As Long)
    Do While position <= Len(jsonText)
        Select Case Mid$(jsonText, position, 1)
            Case " ", vbTab, vbCr, vbLf
                position = position + 1
            Case Else
                Exit Sub
        End Select
    Loop
End Sub

Private Function CharacterAt(ByRef jsonText As String, ByVal position As Long) As String
    If position >= 1 And position <= Len(jsonText) Then
        CharacterAt = Mid$(jsonText, position, 1)
    End If
End Function

Private Function IsDigit(ByVal character As String) As Boolean
    IsDigit = Len(character) = 1 And character >= "0" And character <= "9"
End Function

Private Function IsDigitOneToNine(ByVal character As String) As Boolean
    IsDigitOneToNine = Len(character) = 1 And character >= "1" And character <= "9"
End Function

Private Sub RaiseExpected(ByVal expected As String, ByVal position As Long)
    Err.Raise vbObjectError + 808, "JsonRuntime.ParseJson", _
              "Expected " & expected & " at character " & CStr(position) & "."
End Sub
